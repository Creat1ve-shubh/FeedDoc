// Package rules checks a catalog against Google Merchant Center requirements
// and reports every issue that would get a product disapproved, limit its
// reach, or leave performance on the table.
package rules

import (
	"sort"

	"github.com/Creat1ve-shubh/FeedDoc/internal/catalog"
)

// Severity orders issues by what they cost the merchant.
type Severity int

const (
	// Opportunity: the product is approved but would perform better.
	Opportunity Severity = iota
	// Warning: limited performance, or a disapproval under some conditions.
	Warning
	// Disapproval: Google rejects the product (or, for store issues, the account).
	Disapproval
)

func (s Severity) String() string {
	switch s {
	case Disapproval:
		return "disapproval"
	case Warning:
		return "warning"
	default:
		return "opportunity"
	}
}

// MarshalText makes severities readable in JSON output.
func (s Severity) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// ParseSeverity maps a flag value to a Severity.
func ParseSeverity(s string) (Severity, bool) {
	switch s {
	case "disapproval":
		return Disapproval, true
	case "warning":
		return Warning, true
	case "opportunity", "all":
		return Opportunity, true
	}
	return Opportunity, false
}

// Issue is one problem found on a product, variant or the store.
type Issue struct {
	RuleID      string   `json:"rule"`
	Severity    Severity `json:"severity"`
	ProductID   string   `json:"productId,omitempty"`
	Product     string   `json:"product,omitempty"`
	VariantID   string   `json:"variantId,omitempty"`
	Field       string   `json:"field"`
	Message     string   `json:"message"`
	Fix         string   `json:"fix"`
	AutoFixable bool     `json:"autoFixable"`
}

// Rule is one check. Product rules see one product at a time; store rules
// see the whole catalog (for duplicates and account-level policies).
type Rule struct {
	ID      string
	Summary string
	Product func(st *catalog.Store, p *catalog.Product) []Issue
	Store   func(c *catalog.Catalog) []Issue
}

// Engine runs a set of rules.
type Engine struct {
	rules []Rule
}

// New returns an engine with every built-in rule.
func New() *Engine { return &Engine{rules: All()} }

// Rules lists the rules the engine runs.
func (e *Engine) Rules() []Rule { return e.rules }

// Run checks the catalog and returns issues sorted worst first.
// Draft and archived products are skipped because they are never sent to Google.
func (e *Engine) Run(c *catalog.Catalog) []Issue {
	var out []Issue
	for _, r := range e.rules {
		if r.Store != nil {
			for _, is := range r.Store(c) {
				if is.RuleID == "" {
					is.RuleID = r.ID
				}
				out = append(out, is)
			}
		}
		if r.Product == nil {
			continue
		}
		for i := range c.Products {
			p := &c.Products[i]
			if !p.Live() {
				continue
			}
			for _, is := range r.Product(&c.Store, p) {
				if is.RuleID == "" {
					is.RuleID = r.ID
				}
				if is.ProductID == "" {
					is.ProductID = p.ID
				}
				if is.Product == "" {
					is.Product = p.Title
				}
				out = append(out, is)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Severity != out[j].Severity {
			return out[i].Severity > out[j].Severity
		}
		return out[i].ProductID < out[j].ProductID
	})
	return out
}

// RunProducts checks only the given products with the per-product rules. It
// is the incremental path after a product webhook: catalog-wide rules
// (duplicates, store policies) need the whole catalog and run on full scans.
func (e *Engine) RunProducts(st *catalog.Store, products []catalog.Product) []Issue {
	c := &catalog.Catalog{Store: *st, Products: products}
	var product []Rule
	for _, r := range e.rules {
		if r.Product != nil {
			product = append(product, Rule{ID: r.ID, Summary: r.Summary, Product: r.Product})
		}
	}
	return (&Engine{rules: product}).Run(c)
}

// ProductRuleIDs lists the rules RunProducts covers, so a caller knows which
// stored issues an incremental scan is allowed to mark fixed.
func (e *Engine) ProductRuleIDs() []string {
	var ids []string
	for _, r := range e.rules {
		if r.Product != nil {
			ids = append(ids, r.ID)
		}
	}
	return ids
}
