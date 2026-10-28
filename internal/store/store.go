// Package store keeps shops, scans and issues in Postgres. Each scan is
// diffed against the issues already stored, so every problem has a history:
// when it first appeared, whether it was fixed, and whether it came back.
package store

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Creat1ve-shubh/FeedDoc/internal/catalog"
	"github.com/Creat1ve-shubh/FeedDoc/internal/rules"
)

//go:embed schema.sql
var schema string

// ErrNotFound means the shop is not installed.
var ErrNotFound = errors.New("not found")

// Store is the Postgres-backed persistence layer.
type Store struct {
	db  *pgxpool.Pool
	now func() time.Time
}

// Open connects and applies the schema.
func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	if _, err := db.Exec(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db, now: time.Now}, nil
}

// Close releases the connection pool.
func (s *Store) Close() { s.db.Close() }

// ---- shops -----------------------------------------------------------------

// Shop is an installed store.
type Shop struct {
	Domain       string
	AccessToken  string
	Scope        string
	Info         catalog.Store
	LiveProducts int
	LastFullScan *time.Time
}

// SaveShop records an install or reinstall with its new access token.
func (s *Store) SaveShop(ctx context.Context, domain, token, scope string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO shops (domain, access_token, scope) VALUES ($1, $2, $3)
		ON CONFLICT (domain) DO UPDATE
		SET access_token = $2, scope = $3, installed_at = now(), uninstalled_at = NULL`,
		domain, token, scope)
	return err
}

// Shop loads an installed shop.
func (s *Store) Shop(ctx context.Context, domain string) (Shop, error) {
	var sh Shop
	var info []byte
	err := s.db.QueryRow(ctx, `
		SELECT domain, access_token, scope, store_info, live_products, last_full_scan
		FROM shops WHERE domain = $1 AND uninstalled_at IS NULL`, domain).
		Scan(&sh.Domain, &sh.AccessToken, &sh.Scope, &info, &sh.LiveProducts, &sh.LastFullScan)
	if errors.Is(err, pgx.ErrNoRows) {
		return Shop{}, ErrNotFound
	}
	if err != nil {
		return Shop{}, err
	}
	if len(info) > 0 {
		if err := json.Unmarshal(info, &sh.Info); err != nil {
			return Shop{}, fmt.Errorf("decode store info: %w", err)
		}
	}
	return sh, nil
}

// ShopsDueForFullScan lists installed shops whose last full scan is older
// than every, oldest first.
func (s *Store) ShopsDueForFullScan(ctx context.Context, every time.Duration) ([]string, error) {
	rows, err := s.db.Query(ctx, `
		SELECT domain FROM shops
		WHERE uninstalled_at IS NULL AND (last_full_scan IS NULL OR last_full_scan < $1)
		ORDER BY last_full_scan NULLS FIRST`, s.now().Add(-every))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// Uninstall forgets the access token but keeps the issue history until
// Shopify sends shop/redact, 48 hours later.
func (s *Store) Uninstall(ctx context.Context, domain string) error {
	_, err := s.db.Exec(ctx, `UPDATE shops SET access_token = '', uninstalled_at = now() WHERE domain = $1`, domain)
	return err
}

// Redact deletes everything stored for a shop.
func (s *Store) Redact(ctx context.Context, domain string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM shops WHERE domain = $1`, domain)
	return err
}

// ---- scans -------------------------------------------------------------------

// Result is what one scan changed.
type Result struct {
	Opened    int `json:"opened"`    // problems never seen before
	Reopened  int `json:"reopened"`  // problems that were fixed and came back
	Fixed     int `json:"fixed"`     // open problems no longer found
	StillOpen int `json:"stillOpen"` // open problems found again
	Removed   int `json:"removed"`   // issues of deleted or unpublished products
}

// FullScan is the outcome of checking a whole catalog.
type FullScan struct {
	Started time.Time
	Store   catalog.Store
	// Products is every product exported, live or not.
	Products []catalog.Product
	Issues   []rules.Issue
}

// ApplyFullScan stores a full scan. Every stored issue is in scope: those not
// found again are fixed, and those of products no longer live are removed.
func (s *Store) ApplyFullScan(ctx context.Context, shop string, fs FullScan) (Result, error) {
	live := liveIDs(fs.Products)
	info, err := json.Marshal(fs.Store)
	if err != nil {
		return Result{}, err
	}
	var res Result
	err = s.inTx(ctx, shop, func(tx pgx.Tx) error {
		res, err = s.apply(ctx, tx, shop, fs.Issues, live,
			`SELECT fingerprint, status, product_id FROM issues WHERE shop = $1 FOR UPDATE`, shop)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE shops SET store_info = $2, live_products = $3, last_full_scan = $4 WHERE domain = $1`,
			shop, info, len(live), s.now()); err != nil {
			return err
		}
		return s.recordScan(ctx, tx, shop, "full", fs.Started, len(fs.Products), res)
	})
	return res, err
}

// ApplyProductScan stores an incremental scan of some products. Only issues
// of those products from the given per-product rules are in scope, so a
// catalog-wide issue such as a duplicate SKU stays open until the next full
// scan decides it. checked are the ids that were refetched; any not in
// products were deleted.
func (s *Store) ApplyProductScan(ctx context.Context, shop string, started time.Time, checked []string, products []catalog.Product, issues []rules.Issue, ruleIDs []string) (Result, error) {
	live := liveIDs(products)
	var res Result
	err := s.inTx(ctx, shop, func(tx pgx.Tx) error {
		var err error
		res, err = s.apply(ctx, tx, shop, issues, live, `
			SELECT fingerprint, status, product_id FROM issues
			WHERE shop = $1 AND product_id = ANY($2) AND rule_id = ANY($3) FOR UPDATE`,
			shop, checked, ruleIDs)
		if err != nil {
			return err
		}
		// Catalog-wide issues of a product that is gone go too.
		tag, err := tx.Exec(ctx, `
			DELETE FROM issues WHERE shop = $1 AND product_id = ANY($2) AND NOT (product_id = ANY($3))`,
			shop, checked, keys(live))
		if err != nil {
			return err
		}
		res.Removed += int(tag.RowsAffected())
		return s.recordScan(ctx, tx, shop, "products", started, len(checked), res)
	})
	return res, err
}

// inTx runs fn in a transaction holding the shop's advisory lock, so a full
// scan and a webhook rescan of the same shop never interleave their diffs.
func (s *Store) inTx(ctx context.Context, shop string, fn func(pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, shop); err != nil {
			return err
		}
		return fn(tx)
	})
}

type existing struct {
	status, productID string
}

// apply diffs issues against the stored rows that scopeQuery selects.
func (s *Store) apply(ctx context.Context, tx pgx.Tx, shop string, issues []rules.Issue, live map[string]bool, scopeQuery string, args ...any) (Result, error) {
	var res Result
	now := s.now()

	rows, err := tx.Query(ctx, scopeQuery, args...)
	if err != nil {
		return res, err
	}
	stored := map[string]existing{}
	for rows.Next() {
		var fp string
		var e existing
		if err := rows.Scan(&fp, &e.status, &e.productID); err != nil {
			rows.Close()
			return res, err
		}
		stored[fp] = e
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}

	batch := &pgx.Batch{}
	seen := map[string]bool{}
	for _, is := range issues {
		fp := Fingerprint(is)
		if seen[fp] {
			continue
		}
		seen[fp] = true
		prev, ok := stored[fp]
		switch {
		case !ok:
			res.Opened++
		case prev.status == "fixed":
			res.Reopened++
		default:
			res.StillOpen++
		}
		batch.Queue(`
			INSERT INTO issues (shop, fingerprint, rule_id, severity, product_id, product_title, variant_id,
			                    field, message, fix, auto_fixable, status, first_seen, last_seen)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 'open', $12, $12)
			ON CONFLICT (shop, fingerprint) DO UPDATE SET
			    product_title = EXCLUDED.product_title, message = EXCLUDED.message, fix = EXCLUDED.fix,
			    auto_fixable = EXCLUDED.auto_fixable, last_seen = EXCLUDED.last_seen, status = 'open', fixed_at = NULL,
			    times_reopened = issues.times_reopened + CASE WHEN issues.status = 'fixed' THEN 1 ELSE 0 END`,
			shop, fp, is.RuleID, is.Severity.String(), is.ProductID, is.Product, is.VariantID,
			is.Field, is.Message, is.Fix, is.AutoFixable, now)
	}
	for fp, prev := range stored {
		if seen[fp] {
			continue
		}
		if prev.productID != "" && !live[prev.productID] {
			res.Removed++
			batch.Queue(`DELETE FROM issues WHERE shop = $1 AND fingerprint = $2`, shop, fp)
			continue
		}
		if prev.status == "open" {
			res.Fixed++
			batch.Queue(`UPDATE issues SET status = 'fixed', fixed_at = $3 WHERE shop = $1 AND fingerprint = $2`, shop, fp, now)
		}
	}
	if batch.Len() > 0 {
		if err := tx.SendBatch(ctx, batch).Close(); err != nil {
			return res, err
		}
	}
	return res, nil
}

func (s *Store) recordScan(ctx context.Context, tx pgx.Tx, shop, kind string, started time.Time, checked int, r Result) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO scans (shop, kind, started_at, finished_at, products_checked, opened, reopened, fixed, still_open, removed)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		shop, kind, started, s.now(), checked, r.Opened, r.Reopened, r.Fixed, r.StillOpen, r.Removed)
	return err
}

// Fingerprint identifies the same problem across scans. Messages are left out
// because they embed counts that change while the problem stays the same.
func Fingerprint(is rules.Issue) string {
	return is.RuleID + "|" + is.Severity.String() + "|" + is.ProductID + "|" + is.VariantID + "|" + is.Field
}

func liveIDs(products []catalog.Product) map[string]bool {
	live := map[string]bool{}
	for _, p := range products {
		if p.Live() {
			live[p.ID] = true
		}
	}
	return live
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ---- reading issues for the admin ------------------------------------------------

// StoredIssue is an issue with its history.
type StoredIssue struct {
	rules.Issue
	Status        string     `json:"status"` // open or fixed
	FirstSeen     time.Time  `json:"firstSeen"`
	LastSeen      time.Time  `json:"lastSeen"`
	FixedAt       *time.Time `json:"fixedAt,omitempty"`
	TimesReopened int        `json:"timesReopened"`
}

// Recurring reports whether the problem was fixed before and came back.
func (si StoredIssue) Recurring() bool { return si.TimesReopened > 0 }

// Issues lists a shop's issues, open ones first and worst first. status may
// be "open", "fixed" or "" for both.
func (s *Store) Issues(ctx context.Context, shop, status string) ([]StoredIssue, error) {
	rows, err := s.db.Query(ctx, `
		SELECT rule_id, severity, product_id, product_title, variant_id, field, message, fix, auto_fixable,
		       status, first_seen, last_seen, fixed_at, times_reopened
		FROM issues WHERE shop = $1 AND ($2 = '' OR status = $2)
		ORDER BY status DESC,
		         CASE severity WHEN 'disapproval' THEN 0 WHEN 'warning' THEN 1 ELSE 2 END,
		         product_id, rule_id`, shop, status)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (StoredIssue, error) {
		var si StoredIssue
		var sev string
		err := row.Scan(&si.RuleID, &sev, &si.ProductID, &si.Product, &si.VariantID, &si.Field, &si.Message,
			&si.Fix, &si.AutoFixable, &si.Status, &si.FirstSeen, &si.LastSeen, &si.FixedAt, &si.TimesReopened)
		si.Severity, _ = rules.ParseSeverity(sev)
		return si, err
	})
}

// Summary is the headline numbers for the admin dashboard.
type Summary struct {
	LiveProducts   int            `json:"liveProducts"`
	ProductsAtRisk int            `json:"productsAtRisk"`
	HealthScore    int            `json:"healthScore"`
	Open           map[string]int `json:"open"` // by severity
	Recurring      int            `json:"recurring"`
	FixedLast7Days int            `json:"fixedLast7Days"`
	LastFullScan   *time.Time     `json:"lastFullScan,omitempty"`
}

// Summarize computes the dashboard numbers from stored issues.
func (s *Store) Summarize(ctx context.Context, shop string) (Summary, error) {
	sh, err := s.Shop(ctx, shop)
	if err != nil {
		return Summary{}, err
	}
	sum := Summary{
		LiveProducts: sh.LiveProducts,
		Open:         map[string]int{"disapproval": 0, "warning": 0, "opportunity": 0},
		LastFullScan: sh.LastFullScan,
	}
	rows, err := s.db.Query(ctx, `
		SELECT severity, count(*), count(*) FILTER (WHERE times_reopened > 0)
		FROM issues WHERE shop = $1 AND status = 'open' GROUP BY severity`, shop)
	if err != nil {
		return Summary{}, err
	}
	for rows.Next() {
		var sev string
		var n, rec int
		if err := rows.Scan(&sev, &n, &rec); err != nil {
			rows.Close()
			return Summary{}, err
		}
		sum.Open[sev] = n
		sum.Recurring += rec
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Summary{}, err
	}
	err = s.db.QueryRow(ctx, `
		SELECT
		  (SELECT count(DISTINCT product_id) FROM issues
		    WHERE shop = $1 AND status = 'open' AND severity = 'disapproval' AND product_id <> ''),
		  (SELECT count(*) FROM issues WHERE shop = $1 AND status = 'fixed' AND fixed_at > $2)`,
		shop, s.now().Add(-7*24*time.Hour)).Scan(&sum.ProductsAtRisk, &sum.FixedLast7Days)
	if err != nil {
		return Summary{}, err
	}
	sum.HealthScore = 100
	if sum.LiveProducts > 0 {
		sum.HealthScore = 100 * max(sum.LiveProducts-sum.ProductsAtRisk, 0) / sum.LiveProducts
	}
	return sum, nil
}
