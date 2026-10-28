// Command feeddoctor scans a product catalog for Google Merchant Center issues.
//
//	feeddoctor scan -f testdata/messy_store.json
//	feeddoctor scan -f catalog.json -format json -min warning
//	feeddoctor scan -f bulk-export.jsonl -store store.json
//	feeddoctor rules
//
// Exit codes: 0 no disapprovals, 1 disapprovals found, 2 usage or input error.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Creat1ve-shubh/FeedDoc/internal/catalog"
	"github.com/Creat1ve-shubh/FeedDoc/internal/report"
	"github.com/Creat1ve-shubh/FeedDoc/internal/rules"
	"github.com/Creat1ve-shubh/FeedDoc/internal/shopify"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "scan":
		os.Exit(scan(os.Args[2:]))
	case "rules":
		for _, r := range rules.New().Rules() {
			fmt.Printf("%-24s %s\n", r.ID, r.Summary)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func scan(args []string) int {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	file := fs.String("f", "", "catalog JSON file, or a Shopify bulk export .jsonl, to scan (required)")
	storeFile := fs.String("store", "", "with a .jsonl export: JSON file of store info (name, policies, contact)")
	format := fs.String("format", "text", "output format: text or json")
	minFlag := fs.String("min", "all", "lowest severity to show: disapproval, warning or all")
	_ = fs.Parse(args)

	if *file == "" {
		fmt.Fprintln(os.Stderr, "scan: -f is required")
		return 2
	}
	min, ok := rules.ParseSeverity(*minFlag)
	if !ok {
		fmt.Fprintf(os.Stderr, "scan: -min must be disapproval, warning or all, got %q\n", *minFlag)
		return 2
	}
	c, err := loadCatalog(*file, *storeFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	rep := report.Build(c, rules.New().Run(c), min)
	switch *format {
	case "json":
		if err := rep.WriteJSON(os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
	case "text":
		rep.WriteText(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "scan: -format must be text or json, got %q\n", *format)
		return 2
	}
	if rep.ProductsAtRisk > 0 || rep.Counts["disapproval"] > 0 {
		return 1
	}
	return 0
}

// loadCatalog reads a catalog JSON file, or a Shopify bulk operation export
// plus an optional store-info file.
func loadCatalog(file, storeFile string) (*catalog.Catalog, error) {
	if !strings.HasSuffix(file, ".jsonl") {
		return catalog.Load(file)
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	products, err := shopify.ParseBulkProducts(f)
	if err != nil {
		return nil, err
	}
	c := &catalog.Catalog{Products: products}
	if storeFile == "" {
		fmt.Fprintln(os.Stderr, "note: no -store file, so the store-level checks assume no policies or contact details")
	} else {
		raw, err := os.ReadFile(storeFile)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &c.Store); err != nil {
			return nil, fmt.Errorf("parse store info %s: %w", storeFile, err)
		}
	}
	return c, nil
}

func usage() {
	fmt.Fprintln(os.Stderr, `feeddoctor checks a product catalog against Google Merchant Center rules.

Usage:
  feeddoctor scan -f <catalog.json | export.jsonl> [-store store.json] [-format text|json] [-min disapproval|warning|all]
  feeddoctor rules`)
}
