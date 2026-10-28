package queue

import (
	"context"
	"fmt"
	"os"
	"sort"
	"testing"
)

// testQueue uses FEEDDOCTOR_TEST_REDIS_URL with a per-test key prefix. The
// tests skip when it is unset.
func testQueue(t *testing.T) *Queue {
	t.Helper()
	url := os.Getenv("FEEDDOCTOR_TEST_REDIS_URL")
	if url == "" {
		t.Skip("FEEDDOCTOR_TEST_REDIS_URL not set")
	}
	q, err := New(url, fmt.Sprintf("test:%s:", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	keys, _ := q.r.Keys(ctx, q.prefix+"*").Result()
	if len(keys) > 0 {
		q.r.Del(ctx, keys...)
	}
	t.Cleanup(func() { q.Close() })
	return q
}

func TestBurstsCollapse(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()
	for range 5 {
		if err := q.Enqueue(ctx, "a.myshopify.com", "gid://shopify/Product/1"); err != nil {
			t.Fatal(err)
		}
	}
	q.Enqueue(ctx, "a.myshopify.com", "gid://shopify/Product/2")

	shop, ids, ok, err := q.Next(ctx)
	if err != nil || !ok || shop != "a.myshopify.com" {
		t.Fatalf("Next = %q %v %v %v", shop, ids, ok, err)
	}
	sort.Strings(ids)
	if len(ids) != 2 || ids[0] != "gid://shopify/Product/1" {
		t.Fatalf("ids = %v, want the two distinct products", ids)
	}
	if _, _, ok, _ := q.Next(ctx); ok {
		t.Fatal("queue should be empty")
	}
}

func TestLargeBacklogIsBatched(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()
	var ids []string
	for i := range Batch + 10 {
		ids = append(ids, fmt.Sprintf("gid://shopify/Product/%d", i))
	}
	q.Enqueue(ctx, "a.myshopify.com", ids...)
	_, first, _, _ := q.Next(ctx)
	_, second, ok, _ := q.Next(ctx)
	if len(first) != Batch || len(second) != 10 || !ok {
		t.Fatalf("batches of %d and %d, want %d and 10", len(first), len(second), Batch)
	}
}

func TestFullScanQueue(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()
	q.EnqueueFull(ctx, "a.myshopify.com")
	q.EnqueueFull(ctx, "a.myshopify.com")
	if shop, ok, err := q.NextFull(ctx); !ok || err != nil || shop != "a.myshopify.com" {
		t.Fatalf("NextFull = %q %v %v", shop, ok, err)
	}
	if _, ok, _ := q.NextFull(ctx); ok {
		t.Fatal("duplicate full scan request was not collapsed")
	}
}
