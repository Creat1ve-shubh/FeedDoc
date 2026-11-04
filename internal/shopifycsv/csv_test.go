package shopifycsv

import (
	"bytes"
	"encoding/csv"
	"os"
	"strings"
	"testing"
)

func load(t *testing.T) (*Sheet, []byte) {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/dev_store_products.csv")
	if err != nil {
		t.Fatal(err)
	}
	s, _, err := Parse(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return s, raw
}

func TestParseGroupsRowsByHandle(t *testing.T) {
	raw, _ := os.ReadFile("../../testdata/dev_store_products.csv")
	_, c, err := Parse(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Products) != 6 {
		t.Fatalf("got %d products, want 6", len(c.Products))
	}
	kurta := c.Products[0]
	if kurta.Handle != "cotton-kurta" || len(kurta.Variants) != 2 || len(kurta.Images) != 1 {
		t.Fatalf("kurta parsed wrong: %d variants, %d images", len(kurta.Variants), len(kurta.Images))
	}
	if got := kurta.Variants[1].Option("size"); got != "M" {
		t.Errorf("second variant size = %q, want M (option names come from the first row)", got)
	}
	if kurta.Variants[1].ID != "cotton-kurta#row3" {
		t.Errorf("variant ID = %q, want the file line", kurta.Variants[1].ID)
	}
	if !kurta.Variants[0].InventoryTracked || kurta.Variants[0].InventoryQuantity != 12 {
		t.Errorf("inventory not read: %+v", kurta.Variants[0])
	}
	shawl := c.Products[5]
	if shawl.Live() || shawl.PublishedAt != "" {
		t.Errorf("draft, unpublished shawl read as live or published: %+v", shawl)
	}
}

func TestParseRejectsOtherFiles(t *testing.T) {
	if _, _, err := Parse(strings.NewReader("name,email\nA,a@b.c\n")); err == nil {
		t.Fatal("a non-Shopify CSV was accepted")
	}
}

func TestParseHandlesMetafieldHeadersAndBOM(t *testing.T) {
	in := "\uFEFFHandle,Title,Google Shopping / Gender (product.metafields.mm-google-shopping.gender),Variant Price\n" +
		"tee,Plain Tee,unisex,500\n"
	_, c, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if c.Products[0].Gender != "unisex" {
		t.Fatalf("gender = %q, want unisex", c.Products[0].Gender)
	}
}

func TestSetAddsMissingColumn(t *testing.T) {
	s, _ := load(t)
	row := s.ProductRow["refurbished-laptop-stand"]
	s.Set(row, ColCondition, HeaderCondition, "refurbished")
	if got := s.Get(row, ColCondition); got != "refurbished" {
		t.Fatalf("condition = %q", got)
	}
	for i, r := range s.Rows {
		if len(r) != len(s.Header) {
			t.Fatalf("row %d has %d cells, header has %d", i, len(r), len(s.Header))
		}
	}
}

func TestWriteSubsetKeepsMatchingColumns(t *testing.T) {
	s, _ := load(t)
	var buf bytes.Buffer
	if err := s.WriteSubset(&buf, []string{"cotton-kurta"}, []string{ColTitle}); err != nil {
		t.Fatal(err)
	}
	recs, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	wantHeader := "Handle,Option1 Name,Option1 Value,Option2 Name,Option2 Value,Title"
	if got := strings.Join(recs[0], ","); got != wantHeader {
		t.Fatalf("header = %s, want %s", got, wantHeader)
	}
	if len(recs) != 3 {
		t.Fatalf("got %d rows, want header + 2 kurta variants", len(recs))
	}
	if recs[2][2] != "M" {
		t.Fatalf("second row should be the M variant, got %v", recs[2])
	}
}
