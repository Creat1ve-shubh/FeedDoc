// Package storefront builds a catalog from what any Shopify store serves to
// the public: /meta.json, the /products.json pages, the policy pages and the
// home and contact pages. It needs no app install and no API token.
//
// The public data has no barcodes, inventory, Google category, gender, age
// group or condition, so the rules that need those are reported as unchecked.
package storefront

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/Creat1ve-shubh/FeedDoc/internal/catalog"
)

// Input is what the fetch proxy collected from the store.
type Input struct {
	Domain       string            `json:"domain"`
	Meta         json.RawMessage   `json:"meta"`
	ProductPages []json.RawMessage `json:"productPages"`
	// Policies maps refund, shipping, privacy and terms to whether
	// /policies/<name>-policy (or terms-of-service) answered 200.
	Policies    map[string]bool `json:"policies"`
	HomeHTML    string          `json:"homeHtml"`
	ContactHTML string          `json:"contactHtml"`
	// Truncated is set when the proxy stopped before the last page.
	Truncated bool `json:"truncated"`
}

// Unchecked lists the rules the public data cannot support.
var Unchecked = map[string]string{
	"gtin.invalid":          "Barcodes are not public. Upload the product CSV to check them.",
	"gtin.missing":          "Barcodes are not public. Upload the product CSV to check them.",
	"category.missing":      "The Google product category is not public. Upload the product CSV to check it.",
	"apparel.attributes":    "Gender and age group are not public. Upload the product CSV to check them.",
	"condition.mismatch":    "Condition is not public. Upload the product CSV to check it.",
	"availability.oversell": "Inventory is not public.",
}

type meta struct {
	Name     string `json:"name"`
	Domain   string `json:"domain"`
	Currency string `json:"currency"`
}

type page struct {
	Products []product `json:"products"`
}

type product struct {
	Handle      string          `json:"handle"`
	Title       string          `json:"title"`
	BodyHTML    string          `json:"body_html"`
	Vendor      string          `json:"vendor"`
	ProductType string          `json:"product_type"`
	PublishedAt string          `json:"published_at"`
	Tags        json.RawMessage `json:"tags"` // an array, or a comma-separated string on older themes
	Options     []struct {
		Name string `json:"name"`
	} `json:"options"`
	Images []struct {
		Src    string `json:"src"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
		Alt    string `json:"alt"`
	} `json:"images"`
	Variants []struct {
		ID             json.Number `json:"id"`
		Title          string      `json:"title"`
		Option1        string      `json:"option1"`
		Option2        string      `json:"option2"`
		Option3        string      `json:"option3"`
		SKU            string      `json:"sku"`
		Price          any         `json:"price"`
		CompareAtPrice any         `json:"compare_at_price"`
		Grams          float64     `json:"grams"`
	} `json:"variants"`
}

// Build turns the collected pages into a catalog.
func Build(in Input) (*catalog.Catalog, error) {
	c := &catalog.Catalog{}
	var m meta
	if len(in.Meta) > 0 {
		_ = json.Unmarshal(in.Meta, &m) // a store without meta.json still scans
	}
	c.Store = catalog.Store{
		Name:     m.Name,
		Domain:   firstNonEmpty(m.Domain, in.Domain),
		Currency: m.Currency,
		Policies: catalog.Policies{
			Refund:   in.Policies["refund"],
			Shipping: in.Policies["shipping"],
			Privacy:  in.Policies["privacy"],
			Terms:    in.Policies["terms"],
		},
	}
	c.Store.ContactEmail, c.Store.ContactPhone = contacts(in.HomeHTML + "\n" + in.ContactHTML)

	seen := map[string]bool{}
	for i, raw := range in.ProductPages {
		var pg page
		if err := json.Unmarshal(raw, &pg); err != nil {
			return nil, fmt.Errorf("products page %d is not the JSON Shopify serves: %w", i+1, err)
		}
		for _, sp := range pg.Products {
			if seen[sp.Handle] {
				continue
			}
			seen[sp.Handle] = true
			c.Products = append(c.Products, convert(in.Domain, sp))
		}
	}
	return c, nil
}

func convert(domain string, sp product) catalog.Product {
	p := catalog.Product{
		ID:              sp.Handle,
		Handle:          sp.Handle,
		Title:           sp.Title,
		DescriptionHTML: sp.BodyHTML,
		Vendor:          sp.Vendor,
		ProductType:     sp.ProductType,
		Status:          "ACTIVE", // the endpoint lists published products only
		PublishedAt:     firstNonEmpty(sp.PublishedAt, "published"),
		OnlineStoreURL:  "https://" + domain + "/products/" + sp.Handle,
		Tags:            tags(sp.Tags),
	}
	for _, im := range sp.Images {
		p.Images = append(p.Images, catalog.Image{URL: im.Src, Width: im.Width, Height: im.Height, AltText: im.Alt})
	}
	for _, sv := range sp.Variants {
		v := catalog.Variant{
			ID:             sv.ID.String(),
			Title:          sv.Title,
			SKU:            sv.SKU,
			Price:          money(sv.Price),
			CompareAtPrice: money(sv.CompareAtPrice),
			Weight:         sv.Grams,
			WeightUnit:     "GRAMS",
			Options:        map[string]string{},
		}
		for k, val := range []string{sv.Option1, sv.Option2, sv.Option3} {
			if k < len(sp.Options) && val != "" {
				v.Options[sp.Options[k].Name] = val
			}
		}
		p.Variants = append(p.Variants, v)
	}
	return p
}

// money reads a price, which the endpoint serves as a decimal string ("19.99")
// or, on some stores, as a JSON number.
func money(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return fmt.Sprintf("%.2f", x)
	}
	return ""
}

func tags(raw json.RawMessage) []string {
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		for _, t := range strings.Split(s, ",") {
			if t = strings.TrimSpace(t); t != "" {
				list = append(list, t)
			}
		}
	}
	return list
}

var (
	mailto  = regexp.MustCompile(`(?i)mailto:([^"'?\s>]+@[^"'?\s>]+)`)
	email   = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	tel     = regexp.MustCompile(`(?i)tel:([+0-9][0-9 ()+.-]{5,})`)
	assetEx = regexp.MustCompile(`(?i)\.(png|jpe?g|gif|webp|svg|js|css)$`)

	contactForm = regexp.MustCompile(`(?i)<form[^>]+action="[^"]*/contact[#"?]|name="form_type"\s+value="contact"`)
)

// contacts finds a shopper-facing email and phone in the page HTML.
func contacts(html string) (mail, phone string) {
	if m := mailto.FindStringSubmatch(html); m != nil {
		mail = m[1]
	} else {
		for _, e := range email.FindAllString(html, -1) {
			// Skip asset names like logo@2x.png that look like addresses.
			if !assetEx.MatchString(e) && !strings.Contains(e, "sentry") {
				mail = e
				break
			}
		}
	}
	if m := tel.FindStringSubmatch(html); m != nil {
		phone = strings.TrimSpace(m[1])
	}
	// Shopify's contact form posts to /contact. Google accepts a form as a
	// way to reach the store, so count it when no address is shown.
	if mail == "" && contactForm.MatchString(html) {
		mail = "contact form"
	}
	return mail, phone
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
