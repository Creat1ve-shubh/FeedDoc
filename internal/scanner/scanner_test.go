package scanner

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/Creat1ve-shubh/FeedDoc/internal/catalog"
	"github.com/Creat1ve-shubh/FeedDoc/internal/queue"
	"github.com/Creat1ve-shubh/FeedDoc/internal/rules"
	"github.com/Creat1ve-shubh/FeedDoc/internal/store"
)

const shop = "kavya.myshopify.com"

// fakeSource serves the messy test catalog as if it were a live store.
type fakeSource struct{ c *catalog.Catalog }

func (f fakeSource) StoreInfo(context.Context) (catalog.Store, error) { return f.c.Store, nil }
func (f fakeSource) ExportProducts(context.Context) ([]catalog.Product, error) {
	return f.c.Products, nil
}
func (f fakeSource) Products(_ context.Context, ids []string) ([]catalog.Product, error) {
	var out []catalog.Product
	for _, p := range f.c.Products {
		for _, id := range ids {
			if p.ID == id {
				out = append(out, p)
			}
		}
	}
	return out, nil
}

func TestWorkerRunsFullThenProductScans(t *testing.T) {
	dsn, rurl := os.Getenv("FEEDDOCTOR_TEST_DATABASE_URL"), os.Getenv("FEEDDOCTOR_TEST_REDIS_URL")
	if dsn == "" || rurl == "" {
		t.Skip("FEEDDOCTOR_TEST_DATABASE_URL and FEEDDOCTOR_TEST_REDIS_URL not set")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.Redact(ctx, shop)
	st.SaveShop(ctx, shop, "shpat_x", "read_products")
	q, err := queue.New(rurl, "test:scanner:"+time.Now().Format(time.RFC3339Nano)+":")
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	c, err := catalog.Load("../../testdata/messy_store.json")
	if err != nil {
		t.Fatal(err)
	}
	sc := &Scanner{Store: st, Engine: rules.New(), Source: func(string, string) Source { return fakeSource{c} }, now: time.Now}
	w := &Worker{Scanner: sc, Queue: q, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Idle: time.Millisecond, FullEvery: 24 * time.Hour}

	q.EnqueueFull(ctx, shop)
	if !w.step(ctx) {
		t.Fatal("full scan not picked up")
	}
	open, _ := st.Issues(ctx, shop, "open")
	if len(open) == 0 {
		t.Fatal("full scan stored no issues")
	}

	// The merchant renames the kurta; a webhook queues it.
	c.Products[0].Title = "Handloom Cotton Kurta"
	q.Enqueue(ctx, shop, c.Products[0].ID)
	if !w.step(ctx) {
		t.Fatal("product scan not picked up")
	}
	after, _ := st.Issues(ctx, shop, "open")
	if len(after) != len(open)-2 { // the promotional text and the shouting are both gone
		t.Fatalf("open issues %d -> %d, want 2 fewer", len(open), len(after))
	}
	if w.step(ctx) {
		t.Fatal("queue should be empty")
	}
}
