package webscan

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Creat1ve-shubh/FeedDoc/internal/rules"
	"github.com/Creat1ve-shubh/FeedDoc/internal/shopifycsv"
	"github.com/Creat1ve-shubh/FeedDoc/internal/storefront"
)

const storeName = "FeedDoc Test Store"

func devCSV(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/dev_store_products.csv")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func has(issues []rules.Issue, rule, product string) bool {
	for _, is := range issues {
		if is.RuleID == rule && is.ProductID == product {
			return true
		}
	}
	return false
}

func TestScanCSVFindsTheDevStoreProblems(t *testing.T) {
	res, err := ScanCSV(devCSV(t), Options{StoreName: storeName})
	if err != nil {
		t.Fatal(err)
	}
	issues := res.Report.Issues
	for _, w := range []struct{ rule, product string }{
		{"title.promotional", "cotton-kurta"},
		{"gtin.invalid", "wireless-earbuds-pro"},
		{"price.compare_at", "wireless-earbuds-pro"},
		{"price.invalid", "ceramic-coffee-mug"},
		{"description.missing", "ceramic-coffee-mug"},
		{"condition.mismatch", "refurbished-laptop-stand"},
		{"availability.oversell", "refurbished-laptop-stand"},
		{"brand.missing", "leather-wallet"},
		{"id.duplicate", "ceramic-coffee-mug"},
		{"gtin.missing", "ceramic-coffee-mug"}, // own brand: identifier_exists
	} {
		if !has(issues, w.rule, w.product) {
			t.Errorf("missing %s on %s", w.rule, w.product)
		}
	}
	if has(issues, "store.policies", "") {
		t.Error("a CSV scan reported store policies it cannot see")
	}
	if has(issues, "title.missing", "wool-shawl-draft") || res.Report.ProductsLive != 5 {
		t.Errorf("draft product counted: %d live", res.Report.ProductsLive)
	}
	if len(res.Images) != 1 || !strings.Contains(res.Images[0], "picsum") {
		t.Errorf("images to measure = %v, want the kurta photo", res.Images)
	}
	var unchecked []string
	for _, u := range res.Unchecked {
		unchecked = append(unchecked, u.Rule)
	}
	for _, want := range []string{"store.policies", "category.missing", "apparel.attributes", "image.too_small"} {
		if !strings.Contains(strings.Join(unchecked, ","), want) {
			t.Errorf("%s should be listed as unchecked, got %v", want, unchecked)
		}
	}
	if !res.FixRules["title.promotional|cotton-kurta"] || res.FixRules["gtin.invalid|wireless-earbuds-pro"] {
		t.Errorf("fix rules wrong: %v", res.FixRules)
	}
}

func TestScanCSVUsesMeasuredImages(t *testing.T) {
	res, err := ScanCSV(devCSV(t), Options{ImageSizes: map[string][2]int{
		"https://picsum.photos/id/1062/200/200.jpg": {200, 200},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !has(res.Report.Issues, "image.too_small", "cotton-kurta") {
		t.Error("200 px apparel image not flagged once measured")
	}
	if len(res.Images) != 0 {
		t.Errorf("still asking to measure %v", res.Images)
	}
}

func TestFixCSVClearsWhatItClaims(t *testing.T) {
	text := devCSV(t)
	fixed, err := FixCSV(text, Options{StoreName: storeName})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range fixed.Changes {
		got[c.Rule+"|"+c.Handle] = c.After
	}
	want := map[string]string{
		"title.promotional|cotton-kurta":              "Handloom Cotton Kurta",
		"price.compare_at|wireless-earbuds-pro":       "",
		"condition.mismatch|refurbished-laptop-stand": "refurbished",
		"brand.missing|leather-wallet":                storeName,
		"gtin.missing|ceramic-coffee-mug":             "TRUE",
	}
	for k, v := range want {
		if after, ok := got[k]; !ok || after != v {
			t.Errorf("%s: got %q (present %v), want %q", k, after, ok, v)
		}
	}

	// Merge the import file over the original, as Shopify would, and scan
	// again: every fixed issue must be gone.
	merged := mergeImport(t, text, fixed.ImportCSV)
	after, err := ScanCSV(merged, Options{StoreName: storeName})
	if err != nil {
		t.Fatal(err)
	}
	for k := range want {
		parts := strings.SplitN(k, "|", 2)
		for _, is := range after.Report.Issues {
			if is.RuleID == parts[0] && is.ProductID == parts[1] && !(is.RuleID == "gtin.missing" && is.Field != "identifier_exists") {
				t.Errorf("%s still reported after import: %s", k, is.Message)
			}
		}
	}
	if after.Fixes != 0 {
		t.Errorf("a second fix pass would still change %d cells", after.Fixes)
	}
}

func TestFixCSVImportFileShape(t *testing.T) {
	fixed, err := FixCSV(devCSV(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	recs, err := csv.NewReader(strings.NewReader(fixed.ImportCSV)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	header := strings.Join(recs[0], ",")
	if strings.Contains(header, "Inventory Qty") || strings.Contains(header, "Image Src") {
		t.Errorf("import file carries columns it should leave alone: %s", header)
	}
	if !strings.HasPrefix(header, "Handle,") {
		t.Errorf("import file must start with Handle: %s", header)
	}
}

// mergeImport applies an overwrite import to the original export: cells in
// the import's columns replace the original's, matched by handle and the
// variant's place within the product.
func mergeImport(t *testing.T, original, imp string) string {
	t.Helper()
	sheet, _, err := shopifycsv.Parse(strings.NewReader(original))
	if err != nil {
		t.Fatal(err)
	}
	recs, err := csv.NewReader(strings.NewReader(imp)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	header := recs[0]
	seen := map[string]int{}
	for _, rec := range recs[1:] {
		handle := rec[0]
		var rows []int
		for _, i := range sheet.ProductRows[handle] {
			if i == sheet.ProductRow[handle] || sheet.Get(i, "variant price") != "" || sheet.Get(i, "variant sku") != "" {
				rows = append(rows, i)
			}
		}
		row := rows[seen[handle]]
		seen[handle]++
		for c, h := range header {
			sheet.Set(row, strings.ToLower(h), h, rec[c])
		}
	}
	var buf bytes.Buffer
	if err := sheet.Write(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestScanStore(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/storefront.json")
	if err != nil {
		t.Fatal(err)
	}
	var in storefront.Input
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatal(err)
	}
	res, err := ScanStore(in, Options{})
	if err != nil {
		t.Fatal(err)
	}
	r := res.Report
	if r.Store != "Kettle & Thread" || r.ProductsLive != 2 {
		t.Fatalf("store %q with %d products, want Kettle & Thread with 2 (the repeated page is deduped)", r.Store, r.ProductsLive)
	}
	for _, w := range []struct{ rule, product string }{
		{"store.policies", ""},
		{"title.promotional", "cotton-kurta"},
		{"image.too_small", "cotton-kurta"},
		{"price.compare_at", "cotton-kurta"},
		{"price.invalid", "ceramic-coffee-mug"},
		{"image.missing", "ceramic-coffee-mug"},
	} {
		if !has(r.Issues, w.rule, w.product) {
			t.Errorf("missing %s on %q", w.rule, w.product)
		}
	}
	for _, is := range r.Issues {
		if is.RuleID == "store.policies" && is.Field == "contact details" {
			t.Errorf("contact email on the contact page was missed")
		}
		if is.RuleID == "apparel.attributes" || strings.HasPrefix(is.RuleID, "gtin.") {
			t.Errorf("%s reported from public data that cannot show it", is.RuleID)
		}
	}
}
