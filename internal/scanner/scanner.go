// Package scanner runs the rules engine against a live Shopify store and
// stores the results, either for the whole catalog or for products a webhook
// said changed. Worker drains the queue and schedules nightly full scans.
package scanner

import (
	"context"
	"log/slog"
	"time"

	"github.com/Creat1ve-shubh/FeedDoc/internal/catalog"
	"github.com/Creat1ve-shubh/FeedDoc/internal/queue"
	"github.com/Creat1ve-shubh/FeedDoc/internal/rules"
	"github.com/Creat1ve-shubh/FeedDoc/internal/shopify"
	"github.com/Creat1ve-shubh/FeedDoc/internal/store"
)

// Source is the part of the Shopify client a scan needs.
type Source interface {
	StoreInfo(ctx context.Context) (catalog.Store, error)
	ExportProducts(ctx context.Context) ([]catalog.Product, error)
	Products(ctx context.Context, ids []string) ([]catalog.Product, error)
}

// Scanner scans shops.
type Scanner struct {
	Store  *store.Store
	Engine *rules.Engine
	// Source opens a shop's API with its access token.
	Source func(shop, token string) Source
	now    func() time.Time
}

// New returns a scanner that talks to the real Shopify API.
func New(st *store.Store) *Scanner {
	return &Scanner{
		Store:  st,
		Engine: rules.New(),
		Source: func(shop, token string) Source { return shopify.NewClient(shop, token) },
		now:    time.Now,
	}
}

// Full exports the whole catalog, runs every rule and stores the diff.
func (s *Scanner) Full(ctx context.Context, shop string) (store.Result, error) {
	sh, err := s.Store.Shop(ctx, shop)
	if err != nil {
		return store.Result{}, err
	}
	started := s.now()
	src := s.Source(shop, sh.AccessToken)
	info, err := src.StoreInfo(ctx)
	if err != nil {
		return store.Result{}, err
	}
	products, err := src.ExportProducts(ctx)
	if err != nil {
		return store.Result{}, err
	}
	c := &catalog.Catalog{Store: info, Products: products}
	return s.Store.ApplyFullScan(ctx, shop, store.FullScan{
		Started: started, Store: info, Products: products, Issues: s.Engine.Run(c),
	})
}

// Products refetches the given products and rescans them with the
// per-product rules, using the store info saved by the last full scan.
func (s *Scanner) Products(ctx context.Context, shop string, ids []string) (store.Result, error) {
	sh, err := s.Store.Shop(ctx, shop)
	if err != nil {
		return store.Result{}, err
	}
	started := s.now()
	products, err := s.Source(shop, sh.AccessToken).Products(ctx, ids)
	if err != nil {
		return store.Result{}, err
	}
	issues := s.Engine.RunProducts(&sh.Info, products)
	return s.Store.ApplyProductScan(ctx, shop, started, ids, products, issues, s.Engine.ProductRuleIDs())
}

// Queue is the scan work a worker drains: queue.Queue (Redis) or queue.Memory.
type Queue interface {
	Enqueue(ctx context.Context, shop string, productIDs ...string) error
	Next(ctx context.Context) (shop string, ids []string, ok bool, err error)
	EnqueueFull(ctx context.Context, shop string) error
	NextFull(ctx context.Context) (shop string, ok bool, err error)
}

var (
	_ Queue = (*queue.Queue)(nil)
	_ Queue = (*queue.Memory)(nil)
)

// Worker pulls scan work off the queue.
type Worker struct {
	Scanner *Scanner
	Queue   Queue
	Log     *slog.Logger
	// Idle is how long to wait when the queue is empty. It also sets how long
	// webhook bursts are allowed to pile up before a rescan.
	Idle time.Duration
	// FullEvery is how often each shop gets a full scan.
	FullEvery time.Duration
}

// Run processes work until ctx is cancelled, with n concurrent scans.
func (w *Worker) Run(ctx context.Context, n int) {
	go w.schedule(ctx)
	done := make(chan struct{})
	for range n {
		go func() {
			defer func() { done <- struct{}{} }()
			for ctx.Err() == nil {
				if !w.step(ctx) {
					select {
					case <-ctx.Done():
					case <-time.After(w.Idle):
					}
				}
			}
		}()
	}
	for range n {
		<-done
	}
}

// step does one unit of work and reports whether there was any. Full scans
// go first: they supersede any pending product rescans of the same shop.
func (w *Worker) step(ctx context.Context) bool {
	shop, ok, err := w.Queue.NextFull(ctx)
	if err != nil {
		w.Log.Error("queue", "err", err)
		return false
	}
	if ok {
		res, err := w.Scanner.Full(ctx, shop)
		if err != nil {
			w.Log.Error("full scan failed", "shop", shop, "err", err)
			return true
		}
		w.Log.Info("full scan", "shop", shop, "result", res)
		return true
	}
	shop, ids, ok, err := w.Queue.Next(ctx)
	if err != nil {
		w.Log.Error("queue", "err", err)
		return false
	}
	if !ok {
		return shop != "" // an empty batch still counts as work done
	}
	res, err := w.Scanner.Products(ctx, shop, ids)
	if err == store.ErrNotFound {
		return true // uninstalled since the webhook arrived
	}
	if err != nil {
		w.Log.Error("product scan failed, requeueing", "shop", shop, "products", len(ids), "err", err)
		if err := w.Queue.Enqueue(ctx, shop, ids...); err != nil {
			w.Log.Error("requeue", "err", err)
		}
		return false
	}
	w.Log.Info("product scan", "shop", shop, "products", len(ids), "result", res)
	return true
}

// schedule queues a full scan for every shop that has gone FullEvery
// without one, checking hourly.
func (w *Worker) schedule(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		shops, err := w.Scanner.Store.ShopsDueForFullScan(ctx, w.FullEvery)
		if err != nil {
			w.Log.Error("schedule full scans", "err", err)
		}
		for _, shop := range shops {
			if err := w.Queue.EnqueueFull(ctx, shop); err != nil {
				w.Log.Error("schedule full scan", "shop", shop, "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
