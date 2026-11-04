// Package webscan is the logic behind the free web checker: scan a Shopify
// product CSV or a store's public pages, report what was and was not checked,
// and build the fixed CSV. It is plain Go so it can be tested natively; the
// WebAssembly entry point in cmd/feeddoctor-wasm only moves JSON in and out.
package webscan

import (
	"bytes"
	"sort"
	"strings"

	"github.com/Creat1ve-shubh/FeedDoc/internal/autofix"
	"github.com/Creat1ve-shubh/FeedDoc/internal/catalog"
	"github.com/Creat1ve-shubh/FeedDoc/internal/report"
	"github.com/Creat1ve-shubh/FeedDoc/internal/rules"
	"github.com/Creat1ve-shubh/FeedDoc/internal/shopifycsv"
	"github.com/Creat1ve-shubh/FeedDoc/internal/storefront"
)

// Options come from the page.
type Options struct {
	// StoreName is used in the report and as the vendor for products with
	// none. It also tells gtin.missing which products are the store's own
	// brand.
	StoreName string `json:"storeName"`
	// ImageSizes holds the main images the browser has measured, by URL.
	ImageSizes map[string][2]int `json:"imageSizes"`
}

// Unchecked is a rule the scan could not run, and why.
type Unchecked struct {
	Rule    string `json:"rule"`
	Summary string `json:"summary"`
	Reason  string `json:"reason"`
}

// Result is what the page renders.
type Result struct {
	Source    string         `json:"source"` // "csv" or "store"
	Report    *report.Report `json:"report"`
	Unchecked []Unchecked    `json:"unchecked"`
	// FixRules lists "rule|product" keys the fixed CSV clears.
	FixRules map[string]bool `json:"fixRules"`
	Fixes    int             `json:"fixes"` // cells the fixed CSV changes
	// Images are main image URLs still to measure. The browser loads them
	// and scans again with ImageSizes filled in.
	Images []string `json:"images"`
}

// FixResult is the download.
type FixResult struct {
	Changes []autofix.Change `json:"changes"`
	// ImportCSV holds only the changed products and columns, for Shopify's
	// "Overwrite products with matching handles" import.
	ImportCSV string `json:"importCsv"`
}

// ScanCSV checks a Shopify product export.
func ScanCSV(text string, opt Options) (*Result, error) {
	sheet, c, err := shopifycsv.Parse(strings.NewReader(text))
	if err != nil {
		return nil, err
	}
	c.Store.Name = opt.StoreName
	unchecked := sheet.Unchecked()
	images := applySizes(c, opt.ImageSizes)
	if len(images) > 0 {
		unchecked["image.too_small"] = "Measuring images in your browser."
	}
	res := build("csv", c, unchecked)
	res.Images = images

	// Run the fixes on this copy of the sheet to learn what they clear.
	changes, err := fixAll(sheet, opt)
	if err != nil {
		return nil, err
	}
	res.Fixes = len(changes)
	for _, ch := range changes {
		res.FixRules[ch.Rule+"|"+ch.Handle] = true
	}
	return res, nil
}

// FixCSV applies every safe fix and returns the import file.
func FixCSV(text string, opt Options) (*FixResult, error) {
	sheet, _, err := shopifycsv.Parse(strings.NewReader(text))
	if err != nil {
		return nil, err
	}
	changes, err := fixAll(sheet, opt)
	if err != nil {
		return nil, err
	}
	handles, columns := autofix.Summary(changes)
	var buf bytes.Buffer
	if len(changes) > 0 {
		if err := sheet.WriteSubset(&buf, handles, columns); err != nil {
			return nil, err
		}
	}
	return &FixResult{Changes: changes, ImportCSV: buf.String()}, nil
}

// fixAll applies fixes until a pass changes nothing, because one fix can
// expose another: setting the store name as vendor makes a product own-brand,
// which then needs identifier_exists.
func fixAll(sheet *shopifycsv.Sheet, opt Options) ([]autofix.Change, error) {
	var all []autofix.Change
	for pass := 0; pass < 3; pass++ {
		var buf bytes.Buffer
		if err := sheet.Write(&buf); err != nil {
			return nil, err
		}
		_, c, err := shopifycsv.Parse(&buf)
		if err != nil {
			return nil, err
		}
		c.Store.Name = opt.StoreName
		changes := autofix.Apply(sheet, rules.New().Run(c), autofix.Options{StoreName: opt.StoreName})
		if len(changes) == 0 {
			break
		}
		all = append(all, changes...)
	}
	return all, nil
}

// ScanStore checks a store from its public pages.
func ScanStore(in storefront.Input, opt Options) (*Result, error) {
	c, err := storefront.Build(in)
	if err != nil {
		return nil, err
	}
	if opt.StoreName != "" {
		c.Store.Name = opt.StoreName
	}
	unchecked := map[string]string{}
	for k, v := range storefront.Unchecked {
		unchecked[k] = v
	}
	images := applySizes(c, opt.ImageSizes)
	if len(images) > 0 {
		unchecked["image.too_small"] = "Measuring images in your browser."
	}
	res := build("store", c, unchecked)
	res.Images = images
	return res, nil
}

// applySizes fills in measured image sizes and returns the main images that
// still have none.
func applySizes(c *catalog.Catalog, sizes map[string][2]int) []string {
	var todo []string
	seen := map[string]bool{}
	for i := range c.Products {
		p := &c.Products[i]
		if !p.Live() || len(p.Images) == 0 {
			continue
		}
		img := &p.Images[0]
		if img.Width > 0 && img.Height > 0 {
			continue
		}
		if wh, ok := sizes[img.URL]; ok {
			img.Width, img.Height = wh[0], wh[1]
			continue
		}
		if !seen[img.URL] {
			seen[img.URL] = true
			todo = append(todo, img.URL)
		}
	}
	return todo
}

func build(source string, c *catalog.Catalog, unchecked map[string]string) *Result {
	var issues []rules.Issue
	for _, is := range rules.New().Run(c) {
		if _, skip := unchecked[is.RuleID]; !skip {
			issues = append(issues, is)
		}
	}
	res := &Result{
		Source:   source,
		Report:   report.Build(c, issues, rules.Opportunity),
		FixRules: map[string]bool{},
	}
	if res.Report.Issues == nil {
		res.Report.Issues = []rules.Issue{}
	}
	summaries := map[string]string{}
	for _, r := range rules.New().Rules() {
		summaries[r.ID] = r.Summary
	}
	for id, why := range unchecked {
		res.Unchecked = append(res.Unchecked, Unchecked{Rule: id, Summary: summaries[id], Reason: why})
	}
	sort.Slice(res.Unchecked, func(i, j int) bool { return res.Unchecked[i].Rule < res.Unchecked[j].Rule })
	return res
}
