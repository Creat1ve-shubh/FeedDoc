// Package report turns rule issues into a summary a merchant can act on.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Creat1ve-shubh/FeedDoc/internal/catalog"
	"github.com/Creat1ve-shubh/FeedDoc/internal/rules"
)

// Report is the result of one scan.
type Report struct {
	Store          string         `json:"store"`
	ProductsLive   int            `json:"productsLive"`
	ProductsAtRisk int            `json:"productsAtRisk"` // at least one disapproval
	HealthScore    int            `json:"healthScore"`    // % of live products with no disapproval
	Counts         map[string]int `json:"counts"`         // by severity
	ByRule         map[string]int `json:"byRule"`
	AutoFixable    int            `json:"autoFixable"`
	Issues         []rules.Issue  `json:"issues"`
}

// Build summarizes issues, keeping those at or above min severity.
func Build(c *catalog.Catalog, issues []rules.Issue, min rules.Severity) *Report {
	r := &Report{
		Store:  c.Store.Name,
		Counts: map[string]int{"disapproval": 0, "warning": 0, "opportunity": 0},
		ByRule: map[string]int{},
	}
	for _, p := range c.Products {
		if p.Live() {
			r.ProductsLive++
		}
	}
	risky := map[string]bool{}
	for _, is := range issues {
		if is.Severity == rules.Disapproval && is.ProductID != "" {
			risky[is.ProductID] = true
		}
		if is.Severity < min {
			continue
		}
		r.Counts[is.Severity.String()]++
		r.ByRule[is.RuleID]++
		if is.AutoFixable {
			r.AutoFixable++
		}
		r.Issues = append(r.Issues, is)
	}
	r.ProductsAtRisk = len(risky)
	if r.ProductsLive > 0 {
		r.HealthScore = 100 * (r.ProductsLive - r.ProductsAtRisk) / r.ProductsLive
	} else {
		r.HealthScore = 100
	}
	return r
}

// WriteJSON writes the report as indented JSON.
func (r *Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// WriteText writes a human-readable report grouped by product.
func (r *Report) WriteText(w io.Writer) {
	fmt.Fprintf(w, "Feed health for %s\n", orDash(r.Store))
	fmt.Fprintf(w, "%s\n\n", strings.Repeat("=", 40))
	fmt.Fprintf(w, "Health score     %d%%  (%d of %d live products would be approved)\n",
		r.HealthScore, r.ProductsLive-r.ProductsAtRisk, r.ProductsLive)
	fmt.Fprintf(w, "Disapprovals     %d\n", r.Counts["disapproval"])
	fmt.Fprintf(w, "Warnings         %d\n", r.Counts["warning"])
	fmt.Fprintf(w, "Opportunities    %d\n", r.Counts["opportunity"])
	fmt.Fprintf(w, "Auto-fixable     %d\n\n", r.AutoFixable)

	if len(r.Issues) == 0 {
		fmt.Fprintln(w, "No issues found.")
		return
	}

	// Store-level issues first, then products in order of their worst issue.
	var store []rules.Issue
	groups := map[string][]rules.Issue{}
	var order []string
	for _, is := range r.Issues {
		if is.ProductID == "" {
			store = append(store, is)
			continue
		}
		if _, ok := groups[is.ProductID]; !ok {
			order = append(order, is.ProductID)
		}
		groups[is.ProductID] = append(groups[is.ProductID], is)
	}
	if len(store) > 0 {
		fmt.Fprintln(w, "STORE")
		for _, is := range store {
			writeIssue(w, is)
		}
		fmt.Fprintln(w)
	}
	for _, id := range order {
		g := groups[id]
		sort.SliceStable(g, func(i, j int) bool { return g[i].Severity > g[j].Severity })
		fmt.Fprintf(w, "%s  (%s)\n", orDash(g[0].Product), id)
		for _, is := range g {
			writeIssue(w, is)
		}
		fmt.Fprintln(w)
	}
}

func writeIssue(w io.Writer, is rules.Issue) {
	tag := map[rules.Severity]string{
		rules.Disapproval: "[DISAPPROVAL]",
		rules.Warning:     "[WARNING]    ",
		rules.Opportunity: "[OPPORTUNITY]",
	}[is.Severity]
	auto := ""
	if is.AutoFixable {
		auto = "  (auto-fix)"
	}
	fmt.Fprintf(w, "  %s %s  %s%s\n", tag, is.RuleID, is.Message, auto)
	fmt.Fprintf(w, "                %s\n", "Fix: "+is.Fix)
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(untitled)"
	}
	return s
}
