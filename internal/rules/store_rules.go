package rules

import (
	"fmt"
	"strings"

	"github.com/Creat1ve-shubh/FeedDoc/internal/catalog"
)

// checkStorePolicies covers Google's account-level "misrepresentation" checks.
// Missing these does not disapprove one product; it can suspend the account.
func checkStorePolicies(c *catalog.Catalog) []Issue {
	var out []Issue
	pol := c.Store.Policies
	if !pol.Refund {
		out = append(out, Issue{
			Severity: Disapproval, Field: "refund policy",
			Message: "Store has no refund policy page. Google can suspend the whole Merchant Center account for this.",
			Fix:     "Publish a refund policy in Settings > Policies.",
		})
	}
	if !pol.Shipping {
		out = append(out, Issue{
			Severity: Warning, Field: "shipping policy",
			Message: "Store has no shipping policy page.",
			Fix:     "Publish a shipping policy in Settings > Policies, matching the shipping costs in Merchant Center.",
		})
	}
	if strings.TrimSpace(c.Store.ContactEmail) == "" && strings.TrimSpace(c.Store.ContactPhone) == "" {
		out = append(out, Issue{
			Severity: Disapproval, Field: "contact details",
			Message: "Store shows no email or phone number. Google needs a way for shoppers to reach you.",
			Fix:     "Add a contact email or phone number to the store's contact page and footer.",
		})
	}
	if !pol.Privacy || !pol.Terms {
		out = append(out, Issue{
			Severity: Opportunity, Field: "legal pages",
			Message: "Privacy policy or terms of service is missing.",
			Fix:     "Publish both in Settings > Policies; Shopify can generate templates.",
		})
	}
	return out
}

// checkDuplicates finds SKUs and barcodes reused across live products. Feed
// apps that key offers on SKU overwrite each other, and Google merges offers
// that share a GTIN.
func checkDuplicates(c *catalog.Catalog) []Issue {
	type ref struct {
		product, productID, variantID string
	}
	skus := map[string][]ref{}
	gtins := map[string][]ref{}
	for _, p := range c.Products {
		if !p.Live() {
			continue
		}
		for _, v := range p.Variants {
			r := ref{p.Title, p.ID, v.ID}
			if s := strings.TrimSpace(v.SKU); s != "" {
				skus[strings.ToUpper(s)] = append(skus[strings.ToUpper(s)], r)
			}
			if g := NormalizeGTIN(v.Barcode); g != "" {
				gtins[g] = append(gtins[g], r)
			}
		}
	}
	var out []Issue
	emit := func(kind, field, val string, refs []ref, sev Severity, fix string) {
		if len(refs) < 2 {
			return
		}
		seen := map[string]bool{}
		for _, r := range refs {
			if seen[r.productID] {
				continue // one issue per product, even if several of its variants collide
			}
			seen[r.productID] = true
			out = append(out, Issue{
				Severity: sev, Field: field,
				ProductID: r.productID, Product: r.product, VariantID: r.variantID,
				Message: fmt.Sprintf("%s %q is used by %d variants.", kind, val, len(refs)),
				Fix:     fix,
			})
		}
	}
	for s, refs := range skus {
		emit("SKU", "sku", s, refs, Warning, "Give each variant its own SKU.")
	}
	for g, refs := range gtins {
		emit("Barcode", "gtin", g, refs, Warning, "Each variant needs its own GTIN. Check the packaging for the correct code per size or color.")
	}
	return out
}
