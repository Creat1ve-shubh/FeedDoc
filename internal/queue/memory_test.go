package queue

import (
	"context"
	"fmt"
	"testing"
)

func TestMemoryQueue(t *testing.T) {
	q := NewMemory()
	ctx := context.Background()
	for range 3 {
		q.Enqueue(ctx, "a.myshopify.com", "gid://shopify/Product/1")
	}
	var many []string
	for i := range Batch + 5 {
		many = append(many, fmt.Sprintf("gid://shopify/Product/b%d", i))
	}
	q.Enqueue(ctx, "b.myshopify.com", many...)

	shop, ids, ok, _ := q.Next(ctx)
	if !ok || shop != "a.myshopify.com" || len(ids) != 1 {
		t.Fatalf("first batch: %q %v", shop, ids)
	}
	_, first, _, _ := q.Next(ctx)
	_, second, _, _ := q.Next(ctx)
	if len(first) != Batch || len(second) != 5 {
		t.Fatalf("batches of %d and %d", len(first), len(second))
	}
	if _, _, ok, _ := q.Next(ctx); ok {
		t.Fatal("queue should be empty")
	}

	q.EnqueueFull(ctx, "a.myshopify.com")
	q.EnqueueFull(ctx, "a.myshopify.com")
	if _, ok, _ := q.NextFull(ctx); !ok {
		t.Fatal("full scan missing")
	}
	if _, ok, _ := q.NextFull(ctx); ok {
		t.Fatal("duplicate full scan request was not collapsed")
	}
}
