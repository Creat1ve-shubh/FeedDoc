package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Creat1ve-shubh/FeedDoc/internal/shopify"
	"github.com/Creat1ve-shubh/FeedDoc/internal/store"
)

const shop = "kavya.myshopify.com"

type fakeQueue struct {
	products map[string][]string
	full     []string
}

func (q *fakeQueue) Enqueue(_ context.Context, shop string, ids ...string) error {
	q.products[shop] = append(q.products[shop], ids...)
	return nil
}

func (q *fakeQueue) EnqueueFull(_ context.Context, shop string) error {
	q.full = append(q.full, shop)
	return nil
}

type fakeInstaller struct{ callbacks *[]string }

func (f fakeInstaller) RegisterWebhooks(_ context.Context, url string) error {
	*f.callbacks = append(*f.callbacks, url)
	return nil
}

type fixture struct {
	srv       *Server
	h         http.Handler
	q         *fakeQueue
	st        *store.Store
	callbacks []string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	dsn := os.Getenv("FEEDDOCTOR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("FEEDDOCTOR_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := st.Redact(ctx, shop); err != nil {
		t.Fatal(err)
	}

	// Stands in for https://{shop}/admin/oauth/access_token.
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["code"] != "good-code" || body["client_secret"] != "hush" {
			http.Error(w, "bad code", http.StatusBadRequest)
			return
		}
		io.WriteString(w, `{"access_token":"shpat_new","scope":"read_products"}`)
	}))
	t.Cleanup(oauth.Close)

	f := &fixture{q: &fakeQueue{products: map[string][]string{}}, st: st}
	app := shopify.App{APIKey: "key", APISecret: "hush", Scopes: "read_products", OAuthBase: oauth.URL}
	f.srv = New(app, "https://feeddoctor.example/", st, f.q, slog.New(slog.NewTextHandler(io.Discard, nil)))
	f.srv.Installer = func(string, string) Installer { return fakeInstaller{&f.callbacks} }
	f.h = f.srv.Handler()
	return f
}

func signQuery(q url.Values) url.Values {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, k+"="+q.Get(k))
	}
	mac := hmac.New(sha256.New, []byte("hush"))
	mac.Write([]byte(strings.Join(parts, "&")))
	q.Set("hmac", hex.EncodeToString(mac.Sum(nil)))
	return q
}

func (f *fixture) do(r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w
}

func TestInstallFlow(t *testing.T) {
	f := setup(t)

	// A launch from a shop that has not installed goes to /auth.
	launch := signQuery(url.Values{"shop": {shop}, "timestamp": {"1700000000"}})
	w := f.do(httptest.NewRequest("GET", "/?"+launch.Encode(), nil))
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/auth?shop="+shop {
		t.Fatalf("launch: %d %s", w.Code, w.Header().Get("Location"))
	}

	w = f.do(httptest.NewRequest("GET", "/auth?shop="+shop, nil))
	loc, _ := url.Parse(w.Header().Get("Location"))
	if w.Code != http.StatusFound || loc.Host != shop || loc.Query().Get("redirect_uri") != "https://feeddoctor.example/auth/callback" {
		t.Fatalf("auth redirect: %d %s", w.Code, loc)
	}
	state := loc.Query().Get("state")
	cookie := w.Result().Cookies()[0]

	// The callback with a forged state is refused.
	cb := signQuery(url.Values{"shop": {shop}, "code": {"good-code"}, "state": {"forged"}, "timestamp": {"1700000000"}})
	r := httptest.NewRequest("GET", "/auth/callback?"+cb.Encode(), nil)
	r.AddCookie(cookie)
	if w := f.do(r); w.Code != http.StatusForbidden {
		t.Fatalf("forged state: %d", w.Code)
	}

	cb = signQuery(url.Values{"shop": {shop}, "code": {"good-code"}, "state": {state}, "timestamp": {"1700000000"}})
	r = httptest.NewRequest("GET", "/auth/callback?"+cb.Encode(), nil)
	r.AddCookie(cookie)
	w = f.do(r)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "https://"+shop+"/admin/apps/key" {
		t.Fatalf("callback: %d %s %s", w.Code, w.Header().Get("Location"), w.Body)
	}
	sh, err := f.st.Shop(context.Background(), shop)
	if err != nil || sh.AccessToken != "shpat_new" {
		t.Fatalf("shop not saved: %+v %v", sh, err)
	}
	if len(f.callbacks) != 1 || f.callbacks[0] != "https://feeddoctor.example/webhooks" {
		t.Errorf("webhooks registered at %v", f.callbacks)
	}
	if len(f.q.full) != 1 {
		t.Errorf("first full scan not queued: %v", f.q.full)
	}

	// Now installed, the launch serves the admin.
	w = f.do(httptest.NewRequest("GET", "/?"+launch.Encode(), nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `content="key"`) {
		t.Fatalf("admin launch: %d", w.Code)
	}
}

func webhook(topic, body string, sign bool) *http.Request {
	r := httptest.NewRequest("POST", "/webhooks", strings.NewReader(body))
	r.Header.Set("X-Shopify-Topic", topic)
	r.Header.Set("X-Shopify-Shop-Domain", shop)
	if sign {
		mac := hmac.New(sha256.New, []byte("hush"))
		mac.Write([]byte(body))
		r.Header.Set("X-Shopify-Hmac-Sha256", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	}
	return r
}

func TestWebhooks(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.st.SaveShop(ctx, shop, "shpat_x", "read_products")

	if w := f.do(webhook("products/update", `{"admin_graphql_api_id":"gid://shopify/Product/1"}`, false)); w.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned webhook: %d", w.Code)
	}
	if w := f.do(webhook("products/update", `{"id":1,"admin_graphql_api_id":"gid://shopify/Product/1"}`, true)); w.Code != http.StatusOK {
		t.Fatalf("update: %d", w.Code)
	}
	if w := f.do(webhook("products/delete", `{"id":2}`, true)); w.Code != http.StatusOK {
		t.Fatalf("delete: %d", w.Code)
	}
	if got := f.q.products[shop]; len(got) != 2 || got[1] != "gid://shopify/Product/2" {
		t.Errorf("queued %v", got)
	}
	if w := f.do(webhook("customers/redact", `{}`, true)); w.Code != http.StatusOK {
		t.Fatalf("customers/redact: %d", w.Code)
	}
	if w := f.do(webhook("app/uninstalled", `{}`, true)); w.Code != http.StatusOK {
		t.Fatalf("uninstall: %d", w.Code)
	}
	if _, err := f.st.Shop(ctx, shop); err != store.ErrNotFound {
		t.Errorf("shop still installed after app/uninstalled: %v", err)
	}
}

func sessionToken(shop string, exp time.Time) string {
	enc := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	head := enc(map[string]string{"alg": "HS256"}) + "." +
		enc(map[string]any{"dest": "https://" + shop, "aud": "key", "exp": exp.Unix(), "nbf": exp.Add(-time.Minute).Unix()})
	mac := hmac.New(sha256.New, []byte("hush"))
	mac.Write([]byte(head))
	return head + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func TestAdminAPI(t *testing.T) {
	f := setup(t)
	f.st.SaveShop(context.Background(), shop, "shpat_x", "read_products")

	if w := f.do(httptest.NewRequest("GET", "/api/summary", nil)); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", w.Code)
	}
	r := httptest.NewRequest("GET", "/api/summary", nil)
	r.Header.Set("Authorization", "Bearer "+sessionToken(shop, time.Now().Add(time.Minute)))
	w := f.do(r)
	var sum store.Summary
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &sum) != nil || sum.HealthScore != 100 {
		t.Fatalf("summary: %d %s", w.Code, w.Body)
	}

	r = httptest.NewRequest("GET", "/api/issues", nil)
	r.Header.Set("Authorization", "Bearer "+sessionToken(shop, time.Now().Add(time.Minute)))
	if w := f.do(r); w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("issues: %d %s", w.Code, w.Body)
	}

	r = httptest.NewRequest("POST", "/api/rescan", nil)
	r.Header.Set("Authorization", "Bearer "+sessionToken(shop, time.Now().Add(time.Minute)))
	if w := f.do(r); w.Code != http.StatusAccepted || len(f.q.full) != 1 {
		t.Fatalf("rescan: %d %v", w.Code, f.q.full)
	}

	r = httptest.NewRequest("GET", "/api/summary", nil)
	r.Header.Set("Authorization", "Bearer "+sessionToken(shop, time.Now().Add(-time.Hour)))
	if w := f.do(r); w.Code != http.StatusUnauthorized {
		t.Fatalf("expired token: %d", w.Code)
	}
}
