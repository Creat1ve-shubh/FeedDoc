package shopify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Creat1ve-shubh/FeedDoc/internal/catalog"
)

// APIVersion is the Admin API version the app is built against.
const APIVersion = "2025-10"

// Client calls one store's Admin GraphQL API with an offline access token.
type Client struct {
	Shop  string // example.myshopify.com
	Token string
	HTTP  *http.Client
	// BaseURL overrides https://{Shop} in tests.
	BaseURL string
	// PollEvery is how often a bulk operation's status is checked.
	PollEvery time.Duration
}

// NewClient returns a client with sensible timeouts.
func NewClient(shop, token string) *Client {
	return &Client{Shop: shop, Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}, PollEvery: 2 * time.Second}
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return "https://" + c.Shop
}

// GraphQLError is a top-level or user error returned by the API.
type GraphQLError struct{ Messages []string }

func (e *GraphQLError) Error() string { return "shopify graphql: " + strings.Join(e.Messages, "; ") }

// Do runs a GraphQL query and decodes data into out.
func (c *Client) Do(ctx context.Context, query string, vars map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+"/admin/api/"+APIVersion+"/graphql.json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Shopify-Access-Token", c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("shopify graphql: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return fmt.Errorf("shopify graphql: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("shopify graphql: HTTP %d: %s", resp.StatusCode, truncate(raw, 300))
	}
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("shopify graphql: %w", err)
	}
	if len(env.Errors) > 0 {
		ge := &GraphQLError{}
		for _, e := range env.Errors {
			ge.Messages = append(ge.Messages, e.Message)
		}
		return ge
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(env.Data, out)
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "..."
	}
	return string(b)
}

type userErrors []struct {
	Field   []string `json:"field"`
	Message string   `json:"message"`
}

func (u userErrors) err() error {
	if len(u) == 0 {
		return nil
	}
	ge := &GraphQLError{}
	for _, e := range u {
		ge.Messages = append(ge.Messages, e.Message)
	}
	return ge
}

// ---- store info ------------------------------------------------------------

const shopQuery = `{
  shop {
    name myshopifyDomain currencyCode contactEmail email
    billingAddress { phone }
    shopPolicies { type body }
  }
}`

// StoreInfo fetches the store-level facts the policy rules check.
func (c *Client) StoreInfo(ctx context.Context) (catalog.Store, error) {
	var data struct {
		Shop struct {
			Name           string `json:"name"`
			Domain         string `json:"myshopifyDomain"`
			Currency       string `json:"currencyCode"`
			ContactEmail   string `json:"contactEmail"`
			Email          string `json:"email"`
			BillingAddress *struct {
				Phone string `json:"phone"`
			} `json:"billingAddress"`
			Policies []struct {
				Type string `json:"type"`
				Body string `json:"body"`
			} `json:"shopPolicies"`
		} `json:"shop"`
	}
	if err := c.Do(ctx, shopQuery, nil, &data); err != nil {
		return catalog.Store{}, err
	}
	s := data.Shop
	st := catalog.Store{Name: s.Name, Domain: s.Domain, Currency: s.Currency, ContactEmail: s.ContactEmail}
	if st.ContactEmail == "" {
		st.ContactEmail = s.Email
	}
	if s.BillingAddress != nil {
		st.ContactPhone = s.BillingAddress.Phone
	}
	for _, p := range s.Policies {
		if strings.TrimSpace(p.Body) == "" {
			continue
		}
		switch p.Type {
		case "REFUND_POLICY":
			st.Policies.Refund = true
		case "SHIPPING_POLICY":
			st.Policies.Shipping = true
		case "PRIVACY_POLICY":
			st.Policies.Privacy = true
		case "TERMS_OF_SERVICE":
			st.Policies.Terms = true
		}
	}
	return st, nil
}

// ---- products by id ----------------------------------------------------------

// MaxNodes is the most ids one nodes() query accepts.
const MaxNodes = 250

var productsByIDQuery = `query($ids: [ID!]!) {
  nodes(ids: $ids) {
    ... on Product {` + productFields + `
      media(first: 20) { nodes {` + mediaFields + `
      } }
      variants(first: 100) { nodes {` + variantFields + `
      } }
    }
  }
}`

// Products refetches specific products after a webhook. Ids that no longer
// exist are absent from the result.
func (c *Client) Products(ctx context.Context, ids []string) ([]catalog.Product, error) {
	var out []catalog.Product
	for start := 0; start < len(ids); start += MaxNodes {
		end := min(start+MaxNodes, len(ids))
		var data struct {
			Nodes []*gqlProduct `json:"nodes"`
		}
		if err := c.Do(ctx, productsByIDQuery, map[string]any{"ids": ids[start:end]}, &data); err != nil {
			return nil, err
		}
		for _, p := range data.Nodes {
			if p != nil && p.ID != "" {
				out = append(out, p.toCatalog())
			}
		}
	}
	return out, nil
}

// ---- bulk export -------------------------------------------------------------

const bulkRunMutation = `mutation($q: String!) {
  bulkOperationRunQuery(query: $q) {
    bulkOperation { id status }
    userErrors { field message }
  }
}`

const bulkStatusQuery = `query($id: ID!) {
  node(id: $id) { ... on BulkOperation { id status errorCode objectCount url } }
}`

// ErrBulkFailed means Shopify ended the bulk operation without a result.
var ErrBulkFailed = errors.New("shopify bulk operation failed")

// ExportProducts runs a bulk export of every product and waits for it.
func (c *Client) ExportProducts(ctx context.Context) ([]catalog.Product, error) {
	var run struct {
		Op struct {
			BulkOperation *struct {
				ID string `json:"id"`
			} `json:"bulkOperation"`
			UserErrors userErrors `json:"userErrors"`
		} `json:"bulkOperationRunQuery"`
	}
	if err := c.Do(ctx, bulkRunMutation, map[string]any{"q": BulkProductsQuery}, &run); err != nil {
		return nil, err
	}
	if err := run.Op.UserErrors.err(); err != nil {
		return nil, err
	}
	if run.Op.BulkOperation == nil {
		return nil, ErrBulkFailed
	}
	id := run.Op.BulkOperation.ID

	tick := time.NewTicker(c.PollEvery)
	defer tick.Stop()
	for {
		var st struct {
			Node struct {
				Status    string  `json:"status"`
				ErrorCode *string `json:"errorCode"`
				URL       *string `json:"url"`
			} `json:"node"`
		}
		if err := c.Do(ctx, bulkStatusQuery, map[string]any{"id": id}, &st); err != nil {
			return nil, err
		}
		switch st.Node.Status {
		case "COMPLETED":
			if st.Node.URL == nil { // an empty store produces no file
				return nil, nil
			}
			return c.download(ctx, *st.Node.URL)
		case "FAILED", "CANCELED", "EXPIRED":
			return nil, fmt.Errorf("%w: %s %s", ErrBulkFailed, st.Node.Status, deref(st.Node.ErrorCode))
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-tick.C:
		}
	}
}

func (c *Client) download(ctx context.Context, url string) ([]catalog.Product, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// The result URL is pre-signed storage, so it needs no token, and a big
	// catalog can take longer than the API timeout to download.
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("download bulk result: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download bulk result: HTTP %d", resp.StatusCode)
	}
	return ParseBulkProducts(resp.Body)
}

// ---- webhooks ----------------------------------------------------------------

// WebhookTopics are the subscriptions the app registers on install. The
// compliance topics (customers/*, shop/redact) are configured in the app's
// Partner Dashboard settings instead, as Shopify requires.
var WebhookTopics = []string{"PRODUCTS_CREATE", "PRODUCTS_UPDATE", "PRODUCTS_DELETE", "APP_UNINSTALLED"}

const webhookCreateMutation = `mutation($topic: WebhookSubscriptionTopic!, $url: URL!) {
  webhookSubscriptionCreate(topic: $topic, webhookSubscription: {callbackUrl: $url, format: JSON}) {
    webhookSubscription { id }
    userErrors { field message }
  }
}`

// RegisterWebhooks subscribes every WebhookTopics topic to callbackURL.
// Shopify reports an already-taken address as a user error, which is fine.
func (c *Client) RegisterWebhooks(ctx context.Context, callbackURL string) error {
	for _, topic := range WebhookTopics {
		var data struct {
			Op struct {
				UserErrors userErrors `json:"userErrors"`
			} `json:"webhookSubscriptionCreate"`
		}
		if err := c.Do(ctx, webhookCreateMutation, map[string]any{"topic": topic, "url": callbackURL}, &data); err != nil {
			return fmt.Errorf("register %s: %w", topic, err)
		}
		for _, ue := range data.Op.UserErrors {
			if !strings.Contains(strings.ToLower(ue.Message), "already been taken") {
				return fmt.Errorf("register %s: %s", topic, ue.Message)
			}
		}
	}
	return nil
}
