// Package queue holds rescan work in Redis. Webhooks add changed product ids
// to a per-shop set, so a burst of updates to one product (Shopify sends one
// per inventory change) collapses into a single rescan.
package queue

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// Batch is the most product ids handed out at once; it matches the Admin
// API's nodes() limit so one batch is one refetch.
const Batch = 250

// Queue is the Redis-backed work queue.
type Queue struct {
	r      *redis.Client
	prefix string
}

// New connects to a redis:// URL. prefix namespaces the keys.
func New(url, prefix string) (*Queue, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("redis url: %w", err)
	}
	return &Queue{r: redis.NewClient(opt), prefix: prefix}, nil
}

// Close closes the connection.
func (q *Queue) Close() error { return q.r.Close() }

// Ping checks the connection.
func (q *Queue) Ping(ctx context.Context) error { return q.r.Ping(ctx).Err() }

func (q *Queue) dirtyKey(shop string) string { return q.prefix + "dirty:" + shop }
func (q *Queue) shopsKey() string            { return q.prefix + "rescan:shops" }
func (q *Queue) fullKey() string             { return q.prefix + "full:shops" }

// Enqueue marks products of a shop for rescanning. The ids go in before the
// shop is flagged, so a worker that sees the flag always finds them.
func (q *Queue) Enqueue(ctx context.Context, shop string, productIDs ...string) error {
	if len(productIDs) == 0 {
		return nil
	}
	members := make([]any, len(productIDs))
	for i, id := range productIDs {
		members[i] = id
	}
	_, err := q.r.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.SAdd(ctx, q.dirtyKey(shop), members...)
		p.SAdd(ctx, q.shopsKey(), shop)
		return nil
	})
	return err
}

// Next takes up to Batch product ids of one shop. ok is false when there is
// no work. A shop with more ids left is flagged again for the next call.
func (q *Queue) Next(ctx context.Context) (shop string, ids []string, ok bool, err error) {
	shop, err = q.r.SPop(ctx, q.shopsKey()).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil, false, nil
	}
	if err != nil {
		return "", nil, false, err
	}
	ids, err = q.r.SPopN(ctx, q.dirtyKey(shop), Batch).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return "", nil, false, err
	}
	left, err := q.r.SCard(ctx, q.dirtyKey(shop)).Result()
	if err != nil {
		return "", nil, false, err
	}
	if left > 0 {
		if err := q.r.SAdd(ctx, q.shopsKey(), shop).Err(); err != nil {
			return "", nil, false, err
		}
	}
	return shop, ids, len(ids) > 0, nil
}

// EnqueueFull asks for a full catalog scan of a shop.
func (q *Queue) EnqueueFull(ctx context.Context, shop string) error {
	return q.r.SAdd(ctx, q.fullKey(), shop).Err()
}

// NextFull takes one shop waiting for a full scan.
func (q *Queue) NextFull(ctx context.Context) (shop string, ok bool, err error) {
	shop, err = q.r.SPop(ctx, q.fullKey()).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	return shop, err == nil, err
}
