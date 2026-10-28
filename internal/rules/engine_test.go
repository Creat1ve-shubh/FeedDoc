package rules

import (
	"testing"

	"github.com/Creat1ve-shubh/FeedDoc/internal/catalog"
)

func load(t *testing.T, name string) *catalog.Catalog {
	t.Helper()
	c, err := catalog.Load("../../testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// has reports whether an issue with this rule, product and severity exists.
func has(issues []Issue, rule, product string, sev Severity) bool {
	for _, is := range issues {
		if is.RuleID == rule && is.ProductID == product && is.Severity == sev {
			return true
		}
	}
	return false
}

func TestMessyStoreCatchesEveryPlantedProblem(t *testing.T) {
	issues := New().Run(load(t, "messy_store.json"))
	const (
		kurta  = "gid://shopify/Product/1001"
		buds   = "gid://shopify/Product/1002"
		mug    = "gid://shopify/Product/1003"
		stand  = "gid://shopify/Product/1004"
		wallet = "gid://shopify/Product/1005"
		tote   = "gid://shopify/Product/1006"
		bottle = "gid://shopify/Product/1007"
	)
	want := []struct {
		rule, product string
		sev           Severity
	}{
		{"store.policies", "", Disapproval},
		{"title.promotional", kurta, Disapproval},
		{"title.promotional", kurta, Warning}, // shouting
		{"apparel.attributes", kurta, Disapproval},
		{"image.too_small", kurta, Disapproval},
		{"gtin.missing", kurta, Opportunity},
		{"gtin.invalid", buds, Disapproval},
		{"gtin.missing", buds, Warning},
		{"price.compare_at", buds, Warning},
		{"description.missing", mug, Disapproval},
		{"image.missing", mug, Disapproval},
		{"price.invalid", mug, Disapproval},
		{"category.missing", mug, Opportunity},
		{"title.too_long", stand, Warning},
		{"condition.mismatch", stand, Disapproval},
		{"shipping.weight", stand, Opportunity},
		{"link.unpublished", wallet, Disapproval},
		{"image.placeholder", wallet, Disapproval},
		{"id.duplicate", wallet, Warning},
		{"id.duplicate", tote, Warning},
		{"availability.oversell", tote, Warning},
		{"brand.missing", tote, Warning},
		{"description.missing", tote, Warning}, // too thin
		{"gtin.invalid", bottle, Disapproval},  // restricted prefix
		{"id.duplicate", bottle, Warning},      // shared barcode
	}
	for _, w := range want {
		if !has(issues, w.rule, w.product, w.sev) {
			t.Errorf("missing %s %s on %q", w.sev, w.rule, w.product)
		}
	}
}

func TestDraftProductsAreSkipped(t *testing.T) {
	for _, is := range New().Run(load(t, "messy_store.json")) {
		if is.ProductID == "gid://shopify/Product/1008" {
			t.Fatalf("draft product was checked: %+v", is)
		}
	}
}

func TestWalletNeedsNoSize(t *testing.T) {
	for _, is := range New().Run(load(t, "messy_store.json")) {
		if is.RuleID == "apparel.attributes" && is.ProductID == "gid://shopify/Product/1005" {
			if contains(is.Message, "size") {
				t.Fatalf("wallet should not need a size: %s", is.Message)
			}
		}
	}
}

func TestCleanStoreHasNoProblems(t *testing.T) {
	for _, is := range New().Run(load(t, "clean_store.json")) {
		if is.Severity >= Warning {
			t.Errorf("clean store flagged: %s %s on %s: %s", is.Severity, is.RuleID, is.Product, is.Message)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestRunProductsSkipsCatalogWideRules(t *testing.T) {
	c := load(t, "messy_store.json")
	e := New()
	full := e.Run(c)
	inc := e.RunProducts(&c.Store, c.Products)
	for _, is := range inc {
		if is.RuleID == "store.policies" || is.RuleID == "id.duplicate" {
			t.Errorf("incremental run produced catalog-wide issue %s", is.RuleID)
		}
	}
	perProduct := 0
	for _, is := range full {
		if is.RuleID != "store.policies" && is.RuleID != "id.duplicate" {
			perProduct++
		}
	}
	if perProduct != len(inc) {
		t.Errorf("incremental run found %d issues, full run has %d per-product issues", len(inc), perProduct)
	}
}
