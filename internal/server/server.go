// Package server is the Shopify app's HTTP side: the OAuth install, webhook
// intake, and the JSON API the embedded admin reads.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Creat1ve-shubh/FeedDoc/internal/shopify"
	"github.com/Creat1ve-shubh/FeedDoc/internal/store"
)

// Queue is the scan work the server hands off.
type Queue interface {
	Enqueue(ctx context.Context, shop string, productIDs ...string) error
	EnqueueFull(ctx context.Context, shop string) error
}

// Installer is the Shopify API work done right after an install.
type Installer interface {
	RegisterWebhooks(ctx context.Context, callbackURL string) error
}

// Server handles the app's HTTP routes.
type Server struct {
	App    shopify.App
	AppURL string // public https URL of this server, no trailing slash
	Store  *store.Store
	Queue  Queue
	Log    *slog.Logger
	// Installer opens the API for a newly installed shop.
	Installer func(shop, token string) Installer
	now       func() time.Time
}

// New returns a server that talks to the real Shopify API.
func New(app shopify.App, appURL string, st *store.Store, q Queue, log *slog.Logger) *Server {
	return &Server{
		App: app, AppURL: strings.TrimRight(appURL, "/"), Store: st, Queue: q, Log: log,
		Installer: func(shop, token string) Installer { return shopify.NewClient(shop, token) },
		now:       time.Now,
	}
}

// Handler returns the routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok") })
	mux.HandleFunc("GET /{$}", s.launch)
	mux.HandleFunc("GET /auth", s.auth)
	mux.HandleFunc("GET /auth/callback", s.callback)
	mux.HandleFunc("POST /webhooks", s.webhook)
	mux.HandleFunc("GET /api/summary", s.authed(s.summary))
	mux.HandleFunc("GET /api/issues", s.authed(s.issues))
	mux.HandleFunc("POST /api/rescan", s.authed(s.rescan))
	return mux
}

// ---- install ---------------------------------------------------------------

const stateCookie = "feeddoctor_state"

// launch is where Shopify opens the app. A signed request from a shop that
// has not installed yet goes through OAuth; an installed one gets the admin.
func (s *Server) launch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	shop := q.Get("shop")
	if !ValidLaunch(s.App, q) {
		http.Error(w, "Open this app from your Shopify admin.", http.StatusBadRequest)
		return
	}
	if _, err := s.Store.Shop(r.Context(), shop); errors.Is(err, store.ErrNotFound) {
		http.Redirect(w, r, "/auth?shop="+shop, http.StatusFound)
		return
	} else if err != nil {
		s.fail(w, "load shop", err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Shopify admin frames the app; allow only that shop and the admin.
	w.Header().Set("Content-Security-Policy", "frame-ancestors https://"+shop+" https://admin.shopify.com;")
	writeAdmin(w, s.App.APIKey)
}

// ValidLaunch checks a launch or install request signed by Shopify.
func ValidLaunch(app shopify.App, q map[string][]string) bool {
	shops := q["shop"]
	return len(shops) == 1 && shopify.ValidShop(shops[0]) && app.VerifyQuery(q)
}

func (s *Server) auth(w http.ResponseWriter, r *http.Request) {
	shop := r.URL.Query().Get("shop")
	if !shopify.ValidShop(shop) {
		http.Error(w, "missing or invalid shop", http.StatusBadRequest)
		return
	}
	state, err := nonce()
	if err != nil {
		s.fail(w, "nonce", err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: stateCookie, Value: state, Path: "/auth/callback",
		MaxAge: 600, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, s.App.InstallURL(shop, s.AppURL+"/auth/callback", state), http.StatusFound)
}

func (s *Server) callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	shop := q.Get("shop")
	c, err := r.Cookie(stateCookie)
	if err != nil || c.Value == "" || c.Value != q.Get("state") {
		http.Error(w, "install session expired, open the app again", http.StatusForbidden)
		return
	}
	if !ValidLaunch(s.App, q) {
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}
	ctx := r.Context()
	tok, err := s.App.ExchangeCode(ctx, shop, q.Get("code"))
	if err != nil {
		s.fail(w, "exchange code", err)
		return
	}
	if err := s.Store.SaveShop(ctx, shop, tok.AccessToken, tok.Scope); err != nil {
		s.fail(w, "save shop", err)
		return
	}
	if err := s.Installer(shop, tok.AccessToken).RegisterWebhooks(ctx, s.AppURL+"/webhooks"); err != nil {
		s.fail(w, "register webhooks", err)
		return
	}
	if err := s.Queue.EnqueueFull(ctx, shop); err != nil {
		s.fail(w, "queue first scan", err)
		return
	}
	s.Log.Info("installed", "shop", shop, "scope", tok.Scope)
	http.SetCookie(w, &http.Cookie{Name: stateCookie, Path: "/auth/callback", MaxAge: -1})
	http.Redirect(w, r, "https://"+shop+"/admin/apps/"+s.App.APIKey, http.StatusFound)
}

func nonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ---- webhooks --------------------------------------------------------------

// webhook verifies and routes every topic. It answers fast and leaves the
// scanning to the worker, because Shopify retries anything slower than 5s.
func (s *Server) webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 5<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	if !s.App.VerifyWebhook(body, r.Header.Get("X-Shopify-Hmac-Sha256")) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	shop := r.Header.Get("X-Shopify-Shop-Domain")
	topic := r.Header.Get("X-Shopify-Topic")
	if !shopify.ValidShop(shop) {
		http.Error(w, "invalid shop", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	switch topic {
	case "products/create", "products/update":
		var p struct {
			GID string `json:"admin_graphql_api_id"`
		}
		if err := json.Unmarshal(body, &p); err != nil || p.GID == "" {
			http.Error(w, "bad product payload", http.StatusBadRequest)
			return
		}
		err = s.Queue.Enqueue(ctx, shop, p.GID)
	case "products/delete":
		// Refetching a deleted product returns nothing, which removes its issues.
		var p struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(body, &p); err != nil || p.ID == 0 {
			http.Error(w, "bad product payload", http.StatusBadRequest)
			return
		}
		err = s.Queue.Enqueue(ctx, shop, shopify.ProductGID(p.ID))
	case "app/uninstalled":
		err = s.Store.Uninstall(ctx, shop)
	case "shop/redact":
		err = s.Store.Redact(ctx, shop)
	case "customers/data_request", "customers/redact":
		// The app stores no customer data, so there is nothing to export or erase.
	default:
		s.Log.Warn("unhandled webhook", "topic", topic, "shop", shop)
	}
	if err != nil {
		s.fail(w, "webhook "+topic, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ---- admin API -------------------------------------------------------------

// authed requires an App Bridge session token and passes on its shop.
func (s *Server) authed(h func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			http.Error(w, "missing session token", http.StatusUnauthorized)
			return
		}
		shop, err := s.App.SessionShop(tok, s.now())
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		h(w, r, shop)
	}
}

func (s *Server) summary(w http.ResponseWriter, r *http.Request, shop string) {
	sum, err := s.Store.Summarize(r.Context(), shop)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "shop not installed", http.StatusNotFound)
		return
	}
	if err != nil {
		s.fail(w, "summary", err)
		return
	}
	writeJSON(w, sum)
}

func (s *Server) issues(w http.ResponseWriter, r *http.Request, shop string) {
	status := r.URL.Query().Get("status")
	if status != "" && status != "open" && status != "fixed" {
		http.Error(w, "status must be open or fixed", http.StatusBadRequest)
		return
	}
	is, err := s.Store.Issues(r.Context(), shop, status)
	if err != nil {
		s.fail(w, "issues", err)
		return
	}
	if is == nil {
		is = []store.StoredIssue{}
	}
	writeJSON(w, is)
}

func (s *Server) rescan(w http.ResponseWriter, r *http.Request, shop string) {
	if err := s.Queue.EnqueueFull(r.Context(), shop); err != nil {
		s.fail(w, "queue rescan", err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func (s *Server) fail(w http.ResponseWriter, what string, err error) {
	s.Log.Error(what, "err", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}
