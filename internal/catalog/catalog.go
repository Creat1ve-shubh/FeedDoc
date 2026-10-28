// Package catalog defines the store and product data the rules engine checks.
//
// The shapes mirror what the Shopify Admin GraphQL API returns (prices as
// decimal strings, variants nested under products, Google category stored in
// the mm-google-shopping metafield), so the week-2 Shopify sync can map into
// these types with almost no translation.
package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Catalog is one store's full product export.
type Catalog struct {
	Store    Store     `json:"store"`
	Products []Product `json:"products"`
}

// Store holds store-level facts Google checks at the account level.
type Store struct {
	Name         string   `json:"name"`
	Domain       string   `json:"domain"`
	Currency     string   `json:"currency"`
	ContactEmail string   `json:"contactEmail"`
	ContactPhone string   `json:"contactPhone"`
	Policies     Policies `json:"policies"`
}

// Policies records which legal pages the store has published.
type Policies struct {
	Refund   bool `json:"refund"`
	Shipping bool `json:"shipping"`
	Privacy  bool `json:"privacy"`
	Terms    bool `json:"terms"`
}

// Product is a Shopify product with its variants.
type Product struct {
	ID              string   `json:"id"`
	Handle          string   `json:"handle"`
	Title           string   `json:"title"`
	DescriptionHTML string   `json:"descriptionHtml"`
	Vendor          string   `json:"vendor"`
	ProductType     string   `json:"productType"`
	Status          string   `json:"status"` // ACTIVE, DRAFT, ARCHIVED
	Tags            []string `json:"tags"`
	OnlineStoreURL  string   `json:"onlineStoreUrl"`
	// PublishedAt is when the product was published to the Online Store.
	// It is set even on password-protected stores, where OnlineStoreURL is
	// empty because the storefront is not public.
	PublishedAt    string    `json:"publishedAt"`
	GoogleCategory string    `json:"googleProductCategory"`
	Condition      string    `json:"condition"` // new, refurbished, used
	Gender         string    `json:"gender"`
	AgeGroup       string    `json:"ageGroup"`
	Images         []Image   `json:"images"`
	Variants       []Variant `json:"variants"`
}

// Image is a product image with its pixel dimensions.
type Image struct {
	URL     string `json:"url"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	AltText string `json:"altText"`
}

// Variant is one purchasable option of a product. Each variant becomes one
// offer in Merchant Center.
type Variant struct {
	ID                string            `json:"id"`
	Title             string            `json:"title"`
	SKU               string            `json:"sku"`
	Barcode           string            `json:"barcode"`
	Price             string            `json:"price"`
	CompareAtPrice    string            `json:"compareAtPrice"`
	InventoryTracked  bool              `json:"inventoryTracked"`
	InventoryQuantity int               `json:"inventoryQuantity"`
	InventoryPolicy   string            `json:"inventoryPolicy"` // DENY or CONTINUE
	Weight            float64           `json:"weight"`
	WeightUnit        string            `json:"weightUnit"`
	Options           map[string]string `json:"options"`
}

// Load reads a catalog JSON file.
func Load(path string) (*Catalog, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read catalog: %w", err)
	}
	var c Catalog
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parse catalog %s: %w", path, err)
	}
	return &c, nil
}

// Money parses a Shopify decimal price string. ok is false when the string is
// empty or not a number.
func Money(s string) (value float64, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// Option returns a variant option value by name, case-insensitively.
func (v Variant) Option(name string) string {
	for k, val := range v.Options {
		if strings.EqualFold(k, name) {
			return strings.TrimSpace(val)
		}
	}
	return ""
}

// Live reports whether the product would be sent to Google at all.
func (p Product) Live() bool {
	return strings.EqualFold(p.Status, "ACTIVE") || p.Status == ""
}
