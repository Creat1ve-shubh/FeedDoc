package shopify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// App holds the credentials from the app's Partner Dashboard page.
type App struct {
	APIKey    string
	APISecret string
	Scopes    string // comma separated, e.g. "read_products"
	// OAuthBase overrides https://{shop} for the token exchange in tests.
	OAuthBase string
}

var shopDomain = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*\.myshopify\.com$`)

// ValidShop reports whether s is a bare myshopify.com domain. Every shop
// parameter is checked before it goes into a URL, or the OAuth flow becomes
// an open redirect.
func ValidShop(s string) bool { return shopDomain.MatchString(s) }

// InstallURL is where the merchant is sent to approve the app's scopes.
func (a App) InstallURL(shop, redirectURI, state string) string {
	q := url.Values{
		"client_id":    {a.APIKey},
		"scope":        {a.Scopes},
		"redirect_uri": {redirectURI},
		"state":        {state},
	}
	return "https://" + shop + "/admin/oauth/authorize?" + q.Encode()
}

// VerifyQuery checks the hmac parameter Shopify signs OAuth callbacks and app
// launches with: every other parameter, sorted, joined as k=v with &.
func (a App) VerifyQuery(q url.Values) bool {
	got := q.Get("hmac")
	if got == "" {
		return false
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		if k != "hmac" && k != "signature" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+strings.Join(q[k], ","))
	}
	mac := hmac.New(sha256.New, []byte(a.APISecret))
	mac.Write([]byte(strings.Join(parts, "&")))
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(strings.ToLower(got)))
}

// VerifyWebhook checks X-Shopify-Hmac-Sha256 against the raw request body.
func (a App) VerifyWebhook(body []byte, header string) bool {
	mac := hmac.New(sha256.New, []byte(a.APISecret))
	mac.Write(body)
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return header != "" && hmac.Equal([]byte(want), []byte(header))
}

// Token is an offline access token for one shop.
type Token struct {
	AccessToken string `json:"access_token"`
	Scope       string `json:"scope"`
}

// ExchangeCode trades the OAuth callback's code for an offline access token.
func (a App) ExchangeCode(ctx context.Context, shop, code string) (Token, error) {
	if !ValidShop(shop) {
		return Token{}, fmt.Errorf("invalid shop %q", shop)
	}
	base := "https://" + shop
	if a.OAuthBase != "" {
		base = strings.TrimRight(a.OAuthBase, "/")
	}
	body, _ := json.Marshal(map[string]string{"client_id": a.APIKey, "client_secret": a.APISecret, "code": code})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/admin/oauth/access_token", bytes.NewReader(body))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("exchange code: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return Token{}, fmt.Errorf("exchange code: HTTP %d: %s", resp.StatusCode, truncate(raw, 200))
	}
	var t Token
	if err := json.Unmarshal(raw, &t); err != nil {
		return Token{}, fmt.Errorf("exchange code: %w", err)
	}
	if t.AccessToken == "" {
		return Token{}, errors.New("exchange code: no access token in response")
	}
	return t, nil
}

// ---- session tokens ------------------------------------------------------------

// ErrBadSessionToken covers every way an embedded-admin session token can fail.
var ErrBadSessionToken = errors.New("invalid session token")

// SessionShop verifies the session token App Bridge sends from the embedded
// admin (an HS256 JWT signed with the API secret) and returns its shop.
func (a App) SessionShop(token string, now time.Time) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", ErrBadSessionToken
	}
	var hdr struct {
		Alg string `json:"alg"`
	}
	if err := decodeSegment(parts[0], &hdr); err != nil || hdr.Alg != "HS256" {
		return "", ErrBadSessionToken
	}
	mac := hmac.New(sha256.New, []byte(a.APISecret))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(sig, mac.Sum(nil)) {
		return "", ErrBadSessionToken
	}
	var claims struct {
		Dest string `json:"dest"`
		Aud  string `json:"aud"`
		Exp  int64  `json:"exp"`
		Nbf  int64  `json:"nbf"`
	}
	if err := decodeSegment(parts[1], &claims); err != nil {
		return "", ErrBadSessionToken
	}
	// Allow a few seconds of clock skew between Shopify and this server.
	const skew = 5
	t := now.Unix()
	if claims.Aud != a.APIKey || t > claims.Exp+skew || t < claims.Nbf-skew {
		return "", ErrBadSessionToken
	}
	u, err := url.Parse(claims.Dest)
	if err != nil || !ValidShop(u.Host) {
		return "", ErrBadSessionToken
	}
	return u.Host, nil
}

func decodeSegment(seg string, v any) error {
	raw, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}
