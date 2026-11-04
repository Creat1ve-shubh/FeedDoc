// Package shopifycsv reads the product CSV that Shopify's Products > Export
// button produces, turns it into a catalog the rules engine can check, and
// writes fixes back as a CSV that Shopify's importer accepts.
//
// In that format each product spans several rows that share a Handle. The
// first row carries the product fields; it and the rows after it carry one
// variant each; rows with only an Image Src add more images.
package shopifycsv

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/Creat1ve-shubh/FeedDoc/internal/catalog"
)

// Column names as Shopify writes them, lowercased. Newer exports append the
// metafield key in parentheses ("Google Shopping / Gender (product.metafields
// ...)"), so lookups match on this prefix.
const (
	ColHandle         = "handle"
	ColTitle          = "title"
	ColBody           = "body (html)"
	ColVendor         = "vendor"
	ColType           = "type"
	ColTags           = "tags"
	ColPublished      = "published"
	ColSKU            = "variant sku"
	ColGrams          = "variant grams"
	ColInvTracker     = "variant inventory tracker"
	ColInvQty         = "variant inventory qty"
	ColInvPolicy      = "variant inventory policy"
	ColPrice          = "variant price"
	ColCompareAt      = "variant compare at price"
	ColBarcode        = "variant barcode"
	ColImageSrc       = "image src"
	ColImagePosition  = "image position"
	ColImageAlt       = "image alt text"
	ColStatus         = "status"
	ColGoogleCategory = "google shopping / google product category"
	ColGender         = "google shopping / gender"
	ColAgeGroup       = "google shopping / age group"
	ColCondition      = "google shopping / condition"
	ColCustomProduct  = "google shopping / custom product"

	// Headers for columns a fix adds when the export lacks them.
	HeaderCondition     = "Google Shopping / Condition"
	HeaderCustomProduct = "Google Shopping / Custom Product"
)

// Sheet is the parsed file, kept so fixes can be written back cell by cell.
type Sheet struct {
	Header []string
	Rows   [][]string

	// ProductRow maps a product ID (its handle) to the row holding the
	// product fields. VariantRow maps a variant ID to its row.
	ProductRow map[string]int
	VariantRow map[string]int
	// Rows of each product, image-only rows included, in file order.
	ProductRows map[string][]int
	Handles     []string
}

// Parse reads a Shopify product CSV.
func Parse(r io.Reader) (*Sheet, *catalog.Catalog, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	records, err := cr.ReadAll()
	if err != nil {
		return nil, nil, fmt.Errorf("read CSV: %w", err)
	}
	if len(records) == 0 {
		return nil, nil, errors.New("the file is empty")
	}
	header := records[0]
	if len(header) > 0 {
		header[0] = strings.TrimPrefix(header[0], "\uFEFF") // Excel's byte-order mark
	}
	s := &Sheet{
		Header:      header,
		ProductRow:  map[string]int{},
		VariantRow:  map[string]int{},
		ProductRows: map[string][]int{},
	}
	if s.Col(ColHandle) < 0 || s.Col(ColTitle) < 0 {
		return nil, nil, errors.New("this does not look like a Shopify product export: it has no Handle or Title column")
	}
	for _, rec := range records[1:] {
		for len(rec) < len(header) {
			rec = append(rec, "")
		}
		s.Rows = append(s.Rows, rec)
	}

	c := &catalog.Catalog{}
	index := map[string]int{} // handle -> position in c.Products
	optionNames := map[string][3]string{}
	for i := range s.Rows {
		handle := strings.TrimSpace(s.Get(i, ColHandle))
		if handle == "" {
			continue
		}
		pi, seen := index[handle]
		if !seen {
			pi = len(c.Products)
			index[handle] = pi
			s.ProductRow[handle] = i
			s.Handles = append(s.Handles, handle)
			c.Products = append(c.Products, s.product(i, handle))
			optionNames[handle] = [3]string{s.Get(i, "option1 name"), s.Get(i, "option2 name"), s.Get(i, "option3 name")}
		}
		s.ProductRows[handle] = append(s.ProductRows[handle], i)
		p := &c.Products[pi]

		if src := strings.TrimSpace(s.Get(i, ColImageSrc)); src != "" {
			p.Images = append(p.Images, catalog.Image{URL: src, AltText: s.Get(i, ColImageAlt)})
		}
		if s.isVariantRow(i) {
			v := s.variant(i, handle, optionNames[handle])
			s.VariantRow[v.ID] = i
			p.Variants = append(p.Variants, v)
		}
	}
	return s, c, nil
}

// Col returns the index of a column by its lowercased name, or -1.
func (s *Sheet) Col(name string) int {
	for i, h := range s.Header {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == name || strings.HasPrefix(h, name+" (") {
			return i
		}
	}
	return -1
}

// Has reports whether the export includes a column.
func (s *Sheet) Has(name string) bool { return s.Col(name) >= 0 }

// Get returns a cell, or "" when the column is missing.
func (s *Sheet) Get(row int, name string) string {
	c := s.Col(name)
	if c < 0 || row < 0 || row >= len(s.Rows) {
		return ""
	}
	return strings.TrimSpace(s.Rows[row][c])
}

// Set writes a cell, adding the column at the end when the export lacks it.
// It returns the previous value.
func (s *Sheet) Set(row int, name, header, value string) string {
	c := s.Col(name)
	if c < 0 {
		s.Header = append(s.Header, header)
		for i := range s.Rows {
			s.Rows[i] = append(s.Rows[i], "")
		}
		c = len(s.Header) - 1
	}
	old := s.Rows[row][c]
	s.Rows[row][c] = value
	return old
}

func (s *Sheet) isVariantRow(i int) bool {
	for _, col := range []string{"option1 value", ColSKU, ColPrice} {
		if s.Get(i, col) != "" {
			return true
		}
	}
	return false
}

func (s *Sheet) product(i int, handle string) catalog.Product {
	p := catalog.Product{
		ID:              handle,
		Handle:          handle,
		Title:           s.Get(i, ColTitle),
		DescriptionHTML: s.Get(i, ColBody),
		Vendor:          s.Get(i, ColVendor),
		ProductType:     s.Get(i, ColType),
		GoogleCategory:  s.Get(i, ColGoogleCategory),
		Gender:          s.Get(i, ColGender),
		AgeGroup:        s.Get(i, ColAgeGroup),
		Condition:       strings.ToLower(s.Get(i, ColCondition)),
		Status:          strings.ToUpper(s.Get(i, ColStatus)),
		CustomProduct:   strings.EqualFold(s.Get(i, ColCustomProduct), "true"),
	}
	for _, t := range strings.Split(s.Get(i, ColTags), ",") {
		if t = strings.TrimSpace(t); t != "" {
			p.Tags = append(p.Tags, t)
		}
	}
	// The export writes TRUE when the product is on the Online Store. There
	// is no URL or date in the file, so a marker stands in for the date.
	if !s.Has(ColPublished) || strings.EqualFold(s.Get(i, ColPublished), "true") {
		p.PublishedAt = "published (from CSV)"
	}
	return p
}

func (s *Sheet) variant(i int, handle string, names [3]string) catalog.Variant {
	v := catalog.Variant{
		ID:              fmt.Sprintf("%s#row%d", handle, i+2), // +2: 1-based, after the header
		SKU:             s.Get(i, ColSKU),
		Barcode:         strings.TrimPrefix(s.Get(i, ColBarcode), "'"),
		Price:           s.Get(i, ColPrice),
		CompareAtPrice:  s.Get(i, ColCompareAt),
		InventoryPolicy: strings.ToUpper(s.Get(i, ColInvPolicy)),
		Options:         map[string]string{},
	}
	var values []string
	for k := 0; k < 3; k++ {
		val := s.Get(i, fmt.Sprintf("option%d value", k+1))
		if val == "" {
			continue
		}
		values = append(values, val)
		if names[k] != "" {
			v.Options[names[k]] = val
		}
	}
	v.Title = strings.Join(values, " / ")
	if g, err := strconv.ParseFloat(s.Get(i, ColGrams), 64); err == nil {
		v.Weight, v.WeightUnit = g, "GRAMS"
	}
	// Recent exports leave stock out of this file. Only judge overselling
	// when the quantity is there.
	if s.Has(ColInvQty) && strings.EqualFold(s.Get(i, ColInvTracker), "shopify") {
		if q, err := strconv.Atoi(s.Get(i, ColInvQty)); err == nil {
			v.InventoryTracked, v.InventoryQuantity = true, q
		}
	}
	return v
}

// Unchecked lists the rules this export cannot support, with the reason, so
// the report does not claim a pass it never checked.
func (s *Sheet) Unchecked() map[string]string {
	out := map[string]string{
		"store.policies": "Policies and contact details are not in the product export. Scan the store URL to check them.",
	}
	if !s.Has(ColBarcode) {
		out["gtin.invalid"] = "The export has no Variant Barcode column."
		out["gtin.missing"] = out["gtin.invalid"]
	}
	if !s.Has(ColGoogleCategory) {
		out["category.missing"] = "The export has no Google Shopping columns."
	}
	if !s.Has(ColGender) || !s.Has(ColAgeGroup) {
		out["apparel.attributes"] = "The export has no Google Shopping gender and age group columns."
	}
	if !s.Has(ColInvQty) {
		out["availability.oversell"] = "Inventory is not in this export."
	}
	return out
}

// Write writes the whole sheet as CSV.
func (s *Sheet) Write(w io.Writer) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(s.Header); err != nil {
		return err
	}
	if err := cw.WriteAll(s.Rows); err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

// WriteSubset writes only the given products and columns, for an import with
// "Overwrite products with matching handles". Handle and the option columns
// are always kept so Shopify can match each variant. Image-only rows are left
// out, so the import does not touch images.
func (s *Sheet) WriteSubset(w io.Writer, handles []string, columns []string) error {
	keep := []int{}
	seen := map[int]bool{}
	add := func(name string) {
		if c := s.Col(name); c >= 0 && !seen[c] {
			seen[c] = true
			keep = append(keep, c)
		}
	}
	add(ColHandle)
	for k := 1; k <= 3; k++ {
		add(fmt.Sprintf("option%d name", k))
		add(fmt.Sprintf("option%d value", k))
	}
	for _, name := range columns {
		add(name)
	}
	cw := csv.NewWriter(w)
	pick := func(row []string) []string {
		out := make([]string, len(keep))
		for i, c := range keep {
			out[i] = row[c]
		}
		return out
	}
	if err := cw.Write(pick(s.Header)); err != nil {
		return err
	}
	for _, h := range handles {
		for _, i := range s.ProductRows[h] {
			if i != s.ProductRow[h] && !s.isVariantRow(i) {
				continue
			}
			if err := cw.Write(pick(s.Rows[i])); err != nil {
				return err
			}
		}
	}
	cw.Flush()
	return cw.Error()
}
