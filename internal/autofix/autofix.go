// Package autofix rewrites a Shopify product CSV to clear the issues that can
// be fixed without the merchant's judgment: promotional or all-caps titles,
// titles over 150 characters, compare-at prices that are not a discount,
// own-brand products missing identifier_exists, used or refurbished items
// sold as new, and (when the store name is known) missing vendors.
//
// Issues that need real content, such as an empty description, a missing
// photo or a wrong barcode, are left for the merchant.
package autofix

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Creat1ve-shubh/FeedDoc/internal/rules"
	"github.com/Creat1ve-shubh/FeedDoc/internal/shopifycsv"
)

// Options tune the fixes.
type Options struct {
	// StoreName becomes the vendor of products that have none. Empty skips
	// that fix.
	StoreName string
}

// Change is one cell the fix rewrote.
type Change struct {
	Rule    string `json:"rule"`
	Handle  string `json:"handle"`
	Product string `json:"product"`
	Line    int    `json:"line"` // line in the CSV file, header = 1
	Column  string `json:"column"`
	Before  string `json:"before"`
	After   string `json:"after"`
}

// Rules lists the rules Apply can fix.
var Rules = map[string]bool{
	"title.promotional":  true,
	"title.too_long":     true,
	"price.compare_at":   true,
	"gtin.missing":       true, // only the own-brand case: identifier_exists = no
	"condition.mismatch": true,
	"brand.missing":      true, // only when Options.StoreName is set
}

var refurbishedWords = regexp.MustCompile(`(?i)\b(refurbished|renewed)\b`)

// Apply edits the sheet in place and returns what it changed.
func Apply(s *shopifycsv.Sheet, issues []rules.Issue, opt Options) []Change {
	var out []Change
	done := map[string]bool{}
	set := func(is rules.Issue, row int, col, header, value string) {
		key := fmt.Sprintf("%d|%s", row, col)
		if done[key] {
			return
		}
		done[key] = true
		before := s.Set(row, col, header, value)
		if before == value {
			return
		}
		out = append(out, Change{
			Rule: is.RuleID, Handle: is.ProductID, Product: is.Product,
			Line: row + 2, Column: s.Header[s.Col(col)], Before: before, After: value,
		})
	}

	for _, is := range issues {
		prow, ok := s.ProductRow[is.ProductID]
		if !ok {
			continue
		}
		switch is.RuleID {
		case "title.promotional", "title.too_long":
			title := s.Get(prow, shopifycsv.ColTitle)
			if clean := rules.CleanTitle(title); clean != title {
				set(is, prow, shopifycsv.ColTitle, "Title", clean)
			}
		case "price.compare_at":
			if vrow, ok := s.VariantRow[is.VariantID]; ok {
				set(is, vrow, shopifycsv.ColCompareAt, "Variant Compare At Price", "")
			}
		case "gtin.missing":
			if is.Field == "identifier_exists" {
				set(is, prow, shopifycsv.ColCustomProduct, shopifycsv.HeaderCustomProduct, "TRUE")
			}
		case "condition.mismatch":
			cond := "used"
			if refurbishedWords.MatchString(s.Get(prow, shopifycsv.ColTitle)) {
				cond = "refurbished"
			}
			set(is, prow, shopifycsv.ColCondition, shopifycsv.HeaderCondition, cond)
		case "brand.missing":
			if name := strings.TrimSpace(opt.StoreName); name != "" {
				set(is, prow, shopifycsv.ColVendor, "Vendor", name)
			}
		}
	}
	return out
}

// Summary groups changes for the import file: the products touched and the
// columns that changed, in first-seen order.
func Summary(changes []Change) (handles, columns []string) {
	seenH, seenC := map[string]bool{}, map[string]bool{}
	for _, c := range changes {
		if !seenH[c.Handle] {
			seenH[c.Handle] = true
			handles = append(handles, c.Handle)
		}
		col := strings.ToLower(c.Column)
		if !seenC[col] {
			seenC[col] = true
			columns = append(columns, col)
		}
	}
	return handles, columns
}
