package shopify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Creat1ve-shubh/FeedDoc/internal/catalog"
	"github.com/Creat1ve-shubh/FeedDoc/internal/rules"
)

func readFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/shopify_bulk.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestParseBulkProductsStitchesChildren(t *testing.T) {
	products, err := ParseBulkProducts(strings.NewReader(string(readFixture(t))))
	if err != nil {
		t.Fatal(err)
	}
	if len(products) != 3 {
		t.Fatalf("got %d products, want 3", len(products))
	}
	kurta := products[0]
	if len(kurta.Variants) != 2 || len(kurta.Images) != 1 {
		t.Fatalf("kurta: %d variants, %d images; want 2 and 1 (the empty media line is skipped)", len(kurta.Variants), len(kurta.Images))
	}
	if kurta.Images[0].Width != 200 || kurta.Gender != "female" {
		t.Errorf("kurta fields not mapped: %+v", kurta)
	}
	if kurta.GoogleCategory != "Apparel & Accessories > Clothing > Shirts & Tops" {
		t.Errorf("category fallback not used: %q", kurta.GoogleCategory)
	}
	if v := kurta.Variants[0]; v.Option("size") != "S" || v.Weight != 0.3 || !v.InventoryTracked {
		t.Errorf("variant not mapped: %+v", v)
	}
	stand := products[1]
	if stand.Condition != "new" || stand.OnlineStoreURL != "" || stand.Variants[0].CompareAtPrice != "2499.00" {
		t.Errorf("stand not mapped: %+v", stand)
	}
}

func TestBulkExportRunsThroughEngine(t *testing.T) {
	products, err := ParseBulkProducts(strings.NewReader(string(readFixture(t))))
	if err != nil {
		t.Fatal(err)
	}
	c := &catalog.Catalog{Store: catalog.Store{Name: "Kavya Threads", ContactEmail: "hi@kavya.in", Policies: catalog.Policies{Refund: true, Shipping: true, Privacy: true, Terms: true}}, Products: products}
	got := map[string]bool{}
	for _, is := range rules.New().Run(c) {
		got[is.RuleID+" "+is.ProductID] = true
		if is.RuleID == "link.unpublished" && is.ProductID == "gid://shopify/Product/2001" {
			t.Errorf("published product on a password-protected store flagged as unpublished")
		}
		if is.ProductID == "gid://shopify/Product/2003" {
			t.Errorf("draft product was checked: %+v", is)
		}
	}
	for _, want := range []string{
		"title.promotional gid://shopify/Product/2001",
		"image.too_small gid://shopify/Product/2001",
		"apparel.attributes gid://shopify/Product/2001", // no age group
		"link.unpublished gid://shopify/Product/2002",
		"condition.mismatch gid://shopify/Product/2002",
		"availability.oversell gid://shopify/Product/2002",
		"shipping.weight gid://shopify/Product/2002",
	} {
		if !got[want] {
			t.Errorf("missing issue %s", want)
		}
	}
}

func TestParseBulkProductsRejectsOrphans(t *testing.T) {
	_, err := ParseBulkProducts(strings.NewReader(`{"id":"gid://shopify/ProductVariant/1","__parentId":"gid://shopify/Product/404"}`))
	if err == nil {
		t.Fatal("want an error for a child line with no parent")
	}
}

// fakeShopify answers the bulk run, status and download calls.
func fakeShopify(t *testing.T, fixture []byte) *httptest.Server {
	t.Helper()
	polls := 0
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/result.jsonl" {
			w.Write(fixture)
			return
		}
		if r.Header.Get("X-Shopify-Access-Token") != "shpat_test" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req struct {
			Query string `json:"query"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		switch {
		case strings.Contains(req.Query, "bulkOperationRunQuery"):
			io.WriteString(w, `{"data":{"bulkOperationRunQuery":{"bulkOperation":{"id":"gid://shopify/BulkOperation/1","status":"CREATED"},"userErrors":[]}}}`)
		case strings.Contains(req.Query, "BulkOperation"):
			polls++
			if polls < 2 {
				io.WriteString(w, `{"data":{"node":{"id":"gid://shopify/BulkOperation/1","status":"RUNNING"}}}`)
				return
			}
			io.WriteString(w, `{"data":{"node":{"id":"gid://shopify/BulkOperation/1","status":"COMPLETED","url":"`+srv.URL+`/result.jsonl"}}}`)
		case strings.Contains(req.Query, "shopPolicies"):
			io.WriteString(w, `{"data":{"shop":{"name":"Kavya Threads","myshopifyDomain":"kavya.myshopify.com","currencyCode":"INR","contactEmail":"","email":"owner@kavya.in","billingAddress":{"phone":"+91 98290 00000"},"shopPolicies":[{"type":"REFUND_POLICY","body":"<p>30 days</p>"},{"type":"SHIPPING_POLICY","body":""}]}}}`)
		default:
			io.WriteString(w, `{"errors":[{"message":"unexpected query"}]}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestClientExportAndStoreInfo(t *testing.T) {
	srv := fakeShopify(t, readFixture(t))
	c := NewClient("kavya.myshopify.com", "shpat_test")
	c.BaseURL = srv.URL
	c.PollEvery = time.Millisecond

	products, err := c.ExportProducts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(products) != 3 {
		t.Fatalf("got %d products, want 3", len(products))
	}

	st, err := c.StoreInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.ContactEmail != "owner@kavya.in" || st.ContactPhone == "" || !st.Policies.Refund || st.Policies.Shipping {
		t.Errorf("store info: %+v", st)
	}

	c.Token = "wrong"
	if _, err := c.StoreInfo(context.Background()); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("want HTTP 401 error, got %v", err)
	}
}

func TestValidShop(t *testing.T) {
	for s, want := range map[string]bool{
		"kavya.myshopify.com":          true,
		"kavya-threads.myshopify.com":  true,
		"evil.com":                     false,
		"kavya.myshopify.com.evil.com": false,
		"https://kavya.myshopify.com":  false,
		"":                             false,
	} {
		if ValidShop(s) != want {
			t.Errorf("ValidShop(%q) = %v", s, !want)
		}
	}
}

func TestMissingScopes(t *testing.T) {
	app := App{Scopes: "read_products,read_legal_policies"}
	if got := app.MissingScopes("read_products"); len(got) != 1 || got[0] != "read_legal_policies" {
		t.Errorf("MissingScopes = %v", got)
	}
	if got := app.MissingScopes("write_products,read_legal_policies"); len(got) != 0 {
		t.Errorf("write_ should cover read_: %v", got)
	}
}

func TestVerifyQuery(t *testing.T) {
	app := App{APISecret: "hush"}
	q := url.Values{"code": {"abc"}, "shop": {"kavya.myshopify.com"}, "state": {"n1"}, "timestamp": {"1700000000"}}
	mac := hmac.New(sha256.New, []byte("hush"))
	mac.Write([]byte("code=abc&shop=kavya.myshopify.com&state=n1&timestamp=1700000000"))
	q.Set("hmac", hex.EncodeToString(mac.Sum(nil)))
	if !app.VerifyQuery(q) {
		t.Fatal("valid signature rejected")
	}
	q.Set("shop", "evil.myshopify.com")
	if app.VerifyQuery(q) {
		t.Fatal("tampered query accepted")
	}
}

func TestVerifyWebhook(t *testing.T) {
	app := App{APISecret: "hush"}
	body := []byte(`{"id":1}`)
	mac := hmac.New(sha256.New, []byte("hush"))
	mac.Write(body)
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if !app.VerifyWebhook(body, sig) {
		t.Fatal("valid webhook rejected")
	}
	if app.VerifyWebhook([]byte(`{"id":2}`), sig) || app.VerifyWebhook(body, "") {
		t.Fatal("bad webhook accepted")
	}
}

// signSessionToken builds a token the way App Bridge does.
func signSessionToken(secret string, claims map[string]any) string {
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	head := enc(map[string]string{"alg": "HS256", "typ": "JWT"}) + "." + enc(claims)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(head))
	return head + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func TestSessionShop(t *testing.T) {
	app := App{APIKey: "key", APISecret: "hush"}
	now := time.Unix(1_700_000_000, 0)
	good := map[string]any{"dest": "https://kavya.myshopify.com", "aud": "key", "exp": now.Unix() + 60, "nbf": now.Unix() - 1}
	shop, err := app.SessionShop(signSessionToken("hush", good), now)
	if err != nil || shop != "kavya.myshopify.com" {
		t.Fatalf("good token: %q %v", shop, err)
	}
	expired := map[string]any{"dest": "https://kavya.myshopify.com", "aud": "key", "exp": now.Unix() - 60, "nbf": now.Unix() - 120}
	otherApp := map[string]any{"dest": "https://kavya.myshopify.com", "aud": "other", "exp": now.Unix() + 60}
	for name, tok := range map[string]string{
		"expired":      signSessionToken("hush", expired),
		"other app":    signSessionToken("hush", otherApp),
		"wrong secret": signSessionToken("nope", good),
		"garbage":      "a.b.c",
	} {
		if _, err := app.SessionShop(tok, now); err == nil {
			t.Errorf("%s token accepted", name)
		}
	}
}
