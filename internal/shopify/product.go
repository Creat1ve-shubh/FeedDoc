// Package shopify talks to a Shopify store: the OAuth install, webhook
// verification, the Admin GraphQL API, and mapping its product data into
// catalog types for the rules engine.
package shopify

import (
	"strings"

	"github.com/Creat1ve-shubh/FeedDoc/internal/catalog"
)

// productFields is the product selection shared by the bulk export and the
// per-product refetch. The Google fields live in the metafields the Google &
// YouTube channel writes.
const productFields = `
      id handle title descriptionHtml vendor productType status tags onlineStoreUrl publishedAt
      category { fullName }
      googleCategory: metafield(namespace: "mm-google-shopping", key: "google_product_category") { value }
      condition: metafield(namespace: "mm-google-shopping", key: "condition") { value }
      gender: metafield(namespace: "mm-google-shopping", key: "gender") { value }
      ageGroup: metafield(namespace: "mm-google-shopping", key: "age_group") { value }`

const variantFields = `
          id title sku barcode price compareAtPrice inventoryPolicy inventoryQuantity
          inventoryItem { tracked measurement { weight { value unit } } }
          selectedOptions { name value }`

const mediaFields = `
          ... on MediaImage { id image { url width height altText } }`

// gqlProduct is a product node as the Admin GraphQL API returns it. In a bulk
// export Media and Variants are empty and the children arrive as their own
// lines; in a normal query they are nested.
type gqlProduct struct {
	ID              string   `json:"id"`
	Handle          string   `json:"handle"`
	Title           string   `json:"title"`
	DescriptionHTML string   `json:"descriptionHtml"`
	Vendor          string   `json:"vendor"`
	ProductType     string   `json:"productType"`
	Status          string   `json:"status"`
	Tags            []string `json:"tags"`
	OnlineStoreURL  *string  `json:"onlineStoreUrl"`
	PublishedAt     *string  `json:"publishedAt"`
	Category        *struct {
		FullName string `json:"fullName"`
	} `json:"category"`
	GoogleCategory *metafield             `json:"googleCategory"`
	Condition      *metafield             `json:"condition"`
	Gender         *metafield             `json:"gender"`
	AgeGroup       *metafield             `json:"ageGroup"`
	Media          connection[gqlMedia]   `json:"media"`
	Variants       connection[gqlVariant] `json:"variants"`
}

type metafield struct {
	Value string `json:"value"`
}

type connection[T any] struct {
	Nodes []T `json:"nodes"`
}

type gqlMedia struct {
	ID    string `json:"id"`
	Image *struct {
		URL     string `json:"url"`
		Width   int    `json:"width"`
		Height  int    `json:"height"`
		AltText string `json:"altText"`
	} `json:"image"`
}

type gqlVariant struct {
	ID                string  `json:"id"`
	Title             string  `json:"title"`
	SKU               string  `json:"sku"`
	Barcode           string  `json:"barcode"`
	Price             string  `json:"price"`
	CompareAtPrice    *string `json:"compareAtPrice"`
	InventoryPolicy   string  `json:"inventoryPolicy"`
	InventoryQuantity int     `json:"inventoryQuantity"`
	InventoryItem     *struct {
		Tracked     bool `json:"tracked"`
		Measurement *struct {
			Weight *struct {
				Value float64 `json:"value"`
				Unit  string  `json:"unit"`
			} `json:"weight"`
		} `json:"measurement"`
	} `json:"inventoryItem"`
	SelectedOptions []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"selectedOptions"`
}

func (p gqlProduct) toCatalog() catalog.Product {
	out := catalog.Product{
		ID:              p.ID,
		Handle:          p.Handle,
		Title:           p.Title,
		DescriptionHTML: p.DescriptionHTML,
		Vendor:          p.Vendor,
		ProductType:     p.ProductType,
		Status:          p.Status,
		Tags:            p.Tags,
		OnlineStoreURL:  deref(p.OnlineStoreURL),
		PublishedAt:     deref(p.PublishedAt),
		GoogleCategory:  mf(p.GoogleCategory),
		Condition:       strings.ToLower(mf(p.Condition)),
		Gender:          mf(p.Gender),
		AgeGroup:        mf(p.AgeGroup),
	}
	// Shopify's own taxonomy is derived from Google's, so it is a fair
	// fallback when the merchant never set the Google category explicitly.
	if out.GoogleCategory == "" && p.Category != nil {
		out.GoogleCategory = p.Category.FullName
	}
	for _, m := range p.Media.Nodes {
		addMedia(&out, m)
	}
	for _, v := range p.Variants.Nodes {
		out.Variants = append(out.Variants, v.toCatalog())
	}
	return out
}

// addMedia appends an image; video and 3D media carry no image and are skipped.
func addMedia(p *catalog.Product, m gqlMedia) {
	if m.Image == nil || m.Image.URL == "" {
		return
	}
	p.Images = append(p.Images, catalog.Image{
		URL: m.Image.URL, Width: m.Image.Width, Height: m.Image.Height, AltText: m.Image.AltText,
	})
}

func (v gqlVariant) toCatalog() catalog.Variant {
	out := catalog.Variant{
		ID:                v.ID,
		Title:             v.Title,
		SKU:               v.SKU,
		Barcode:           v.Barcode,
		Price:             v.Price,
		CompareAtPrice:    deref(v.CompareAtPrice),
		InventoryQuantity: v.InventoryQuantity,
		InventoryPolicy:   v.InventoryPolicy,
		Options:           map[string]string{},
	}
	if it := v.InventoryItem; it != nil {
		out.InventoryTracked = it.Tracked
		if it.Measurement != nil && it.Measurement.Weight != nil {
			out.Weight = it.Measurement.Weight.Value
			out.WeightUnit = it.Measurement.Weight.Unit
		}
	}
	for _, o := range v.SelectedOptions {
		out.Options[o.Name] = o.Value
	}
	return out
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func mf(m *metafield) string {
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m.Value)
}
