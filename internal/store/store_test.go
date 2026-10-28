package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Creat1ve-shubh/FeedDoc/internal/catalog"
	"github.com/Creat1ve-shubh/FeedDoc/internal/rules"
)

// testStore connects to FEEDDOCTOR_TEST_DATABASE_URL, a throwaway database
// whose tables are wiped first. The tests skip when it is unset.
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("FEEDDOCTOR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("FEEDDOCTOR_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(ctx, `TRUNCATE shops CASCADE`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

const shop = "kavya.myshopify.com"

func loadMessy(t *testing.T) *catalog.Catalog {
	t.Helper()
	c, err := catalog.Load("../../testdata/messy_store.json")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func fullScan(t *testing.T, s *Store, c *catalog.Catalog) Result {
	t.Helper()
	res, err := s.ApplyFullScan(context.Background(), shop, FullScan{
		Started: s.now(), Store: c.Store, Products: c.Products, Issues: rules.New().Run(c),
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func product(c *catalog.Catalog, id string) *catalog.Product {
	for i := range c.Products {
		if c.Products[i].ID == id {
			return &c.Products[i]
		}
	}
	return nil
}

func TestFullScansTrackNewFixedAndRecurring(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	clock := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return clock }
	if err := s.SaveShop(ctx, shop, "shpat_x", "read_products"); err != nil {
		t.Fatal(err)
	}

	c := loadMessy(t)
	first := fullScan(t, s, c)
	if first.Opened == 0 || first.Fixed != 0 || first.Reopened != 0 {
		t.Fatalf("first scan: %+v", first)
	}

	// The same catalog again changes nothing.
	clock = clock.Add(time.Hour)
	again := fullScan(t, s, c)
	if again.Opened != 0 || again.StillOpen != first.Opened || again.Fixed != 0 {
		t.Fatalf("repeat scan: %+v (first opened %d)", again, first.Opened)
	}

	// The merchant fixes the mug's price; one issue is fixed.
	clock = clock.Add(time.Hour)
	mug := product(c, "gid://shopify/Product/1003")
	mug.Variants[0].Price = "499.00"
	fixed := fullScan(t, s, c)
	if fixed.Fixed != 1 || fixed.Opened != 0 {
		t.Fatalf("after fixing the price: %+v", fixed)
	}

	// The price breaks again: the issue reopens instead of counting as new.
	clock = clock.Add(time.Hour)
	mug.Variants[0].Price = "0.00"
	back := fullScan(t, s, c)
	if back.Reopened != 1 || back.Opened != 0 {
		t.Fatalf("after the price broke again: %+v", back)
	}
	open, err := s.Issues(ctx, shop, "open")
	if err != nil {
		t.Fatal(err)
	}
	recurring := 0
	for _, is := range open {
		if is.Recurring() {
			recurring++
			if is.RuleID != "price.invalid" || !is.FirstSeen.Equal(time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)) {
				t.Errorf("wrong recurring issue: %+v", is)
			}
		}
	}
	if recurring != 1 {
		t.Errorf("want 1 recurring issue, got %d", recurring)
	}

	// Drafting a product removes its issues rather than calling them fixed.
	clock = clock.Add(time.Hour)
	mug.Status = "DRAFT"
	gone := fullScan(t, s, c)
	if gone.Removed == 0 || gone.Fixed != 0 {
		t.Fatalf("after drafting the mug: %+v", gone)
	}

	sum, err := s.Summarize(ctx, shop)
	if err != nil {
		t.Fatal(err)
	}
	if sum.LiveProducts != 6 || sum.Open["disapproval"] == 0 || sum.HealthScore >= 100 || sum.FixedLast7Days != 0 {
		t.Errorf("summary: %+v", sum)
	}
}

func TestProductScanOnlyTouchesItsProducts(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.SaveShop(ctx, shop, "shpat_x", "read_products"); err != nil {
		t.Fatal(err)
	}
	c := loadMessy(t)
	fullScan(t, s, c)
	before, _ := s.Issues(ctx, shop, "open")

	// A webhook says the earbuds changed; the merchant fixed the barcode.
	buds := *product(c, "gid://shopify/Product/1002")
	buds.Variants[0].Barcode = "4006381333931"
	e := rules.New()
	issues := e.RunProducts(&c.Store, []catalog.Product{buds})
	res, err := s.ApplyProductScan(ctx, shop, s.now(), []string{buds.ID}, []catalog.Product{buds}, issues, e.ProductRuleIDs())
	if err != nil {
		t.Fatal(err)
	}
	if res.Fixed != 1 || res.Opened != 0 {
		t.Fatalf("earbuds rescan: %+v", res)
	}
	after, _ := s.Issues(ctx, shop, "open")
	if len(after) != len(before)-1 {
		t.Fatalf("open issues went from %d to %d, want one fewer", len(before), len(after))
	}
	// The store-level policy issue was out of scope and is still open.
	found := false
	for _, is := range after {
		found = found || is.RuleID == "store.policies"
	}
	if !found {
		t.Error("incremental scan closed a store-level issue")
	}

	// The tote is deleted in Shopify: refetching returns nothing.
	tote := "gid://shopify/Product/1006"
	res, err = s.ApplyProductScan(ctx, shop, s.now(), []string{tote}, nil, nil, e.ProductRuleIDs())
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed == 0 || res.Fixed != 0 {
		t.Fatalf("deleted product: %+v", res)
	}
	all, _ := s.Issues(ctx, shop, "")
	for _, is := range all {
		if is.ProductID == tote {
			t.Errorf("issue of a deleted product survived: %+v", is)
		}
	}
}

func TestUninstallAndRedact(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.SaveShop(ctx, shop, "shpat_x", "read_products"); err != nil {
		t.Fatal(err)
	}
	fullScan(t, s, loadMessy(t))
	if err := s.Uninstall(ctx, shop); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Shop(ctx, shop); err != ErrNotFound {
		t.Fatalf("uninstalled shop still loads: %v", err)
	}
	due, _ := s.ShopsDueForFullScan(ctx, time.Hour)
	if len(due) != 0 {
		t.Errorf("uninstalled shop is due for a scan: %v", due)
	}
	// Reinstalling within 48 hours keeps the history.
	if err := s.SaveShop(ctx, shop, "shpat_y", "read_products"); err != nil {
		t.Fatal(err)
	}
	if is, _ := s.Issues(ctx, shop, ""); len(is) == 0 {
		t.Error("reinstall lost the issue history")
	}
	if err := s.Redact(ctx, shop); err != nil {
		t.Fatal(err)
	}
	if is, _ := s.Issues(ctx, shop, ""); len(is) != 0 {
		t.Errorf("redact left %d issues", len(is))
	}
}
