// Command feeddoctor-server runs the Shopify app: the install flow, webhook
// intake, the embedded admin, and the scan worker, in one process.
//
// Configuration comes from the environment; see .env.example.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/Creat1ve-shubh/FeedDoc/internal/queue"
	"github.com/Creat1ve-shubh/FeedDoc/internal/scanner"
	"github.com/Creat1ve-shubh/FeedDoc/internal/server"
	"github.com/Creat1ve-shubh/FeedDoc/internal/shopify"
	"github.com/Creat1ve-shubh/FeedDoc/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("exit", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.databaseURL)
	if err != nil {
		return err
	}
	defer st.Close()
	var q scanner.Queue
	if cfg.redisURL == "" {
		log.Warn("REDIS_URL not set, using an in-memory queue: pending rescans are lost on restart")
		q = queue.NewMemory()
	} else {
		rq, err := queue.New(cfg.redisURL, "feeddoctor:")
		if err != nil {
			return err
		}
		defer rq.Close()
		if err := rq.Ping(ctx); err != nil {
			return fmt.Errorf("redis: %w", err)
		}
		q = rq
	}

	app := shopify.App{APIKey: cfg.apiKey, APISecret: cfg.apiSecret, Scopes: cfg.scopes}
	srv := &http.Server{
		Addr:              ":" + cfg.port,
		Handler:           server.New(app, cfg.appURL, st, q, log).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	worker := &scanner.Worker{Scanner: scanner.New(st), Queue: q, Log: log, Idle: 5 * time.Second, FullEvery: 24 * time.Hour}
	go worker.Run(ctx, cfg.workers)

	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	log.Info("listening", "addr", srv.Addr, "app_url", cfg.appURL)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

type config struct {
	apiKey, apiSecret, scopes, appURL string
	databaseURL, redisURL, port       string
	workers                           int
}

func loadConfig() (config, error) {
	c := config{
		apiKey:    os.Getenv("SHOPIFY_API_KEY"),
		apiSecret: os.Getenv("SHOPIFY_API_SECRET"),
		scopes:    envOr("SHOPIFY_SCOPES", "read_products"),
		// Render sets RENDER_EXTERNAL_URL itself, so APP_URL is only needed
		// elsewhere or to override it.
		appURL:      envOr("APP_URL", os.Getenv("RENDER_EXTERNAL_URL")),
		databaseURL: os.Getenv("DATABASE_URL"),
		redisURL:    os.Getenv("REDIS_URL"), // empty: in-memory queue
		port:        envOr("PORT", "8080"),
		workers:     4,
	}
	if w := os.Getenv("SCAN_WORKERS"); w != "" {
		n, err := strconv.Atoi(w)
		if err != nil || n < 1 {
			return c, fmt.Errorf("SCAN_WORKERS must be a positive number, got %q", w)
		}
		c.workers = n
	}
	for name, v := range map[string]string{
		"SHOPIFY_API_KEY": c.apiKey, "SHOPIFY_API_SECRET": c.apiSecret, "APP_URL": c.appURL, "DATABASE_URL": c.databaseURL,
	} {
		if v == "" {
			return c, fmt.Errorf("%s is required", name)
		}
	}
	return c, nil
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}
