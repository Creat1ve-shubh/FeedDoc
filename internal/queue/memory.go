package queue

import (
	"context"
	"sync"
)

// Memory is an in-process queue with the same behaviour as the Redis one.
// It is for running on one machine without Redis: pending work is lost when
// the process stops, and the nightly full scans re-cover anything dropped.
type Memory struct {
	mu    sync.Mutex
	dirty map[string]map[string]bool
	shops []string // shops with dirty products, in arrival order
	full  []string
}

// NewMemory returns an empty in-memory queue.
func NewMemory() *Memory { return &Memory{dirty: map[string]map[string]bool{}} }

// Enqueue marks products of a shop for rescanning.
func (m *Memory) Enqueue(_ context.Context, shop string, productIDs ...string) error {
	if len(productIDs) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	set, ok := m.dirty[shop]
	if !ok {
		set = map[string]bool{}
		m.dirty[shop] = set
		m.shops = append(m.shops, shop)
	}
	for _, id := range productIDs {
		set[id] = true
	}
	return nil
}

// Next takes up to Batch product ids of one shop.
func (m *Memory) Next(_ context.Context) (shop string, ids []string, ok bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.shops) == 0 {
		return "", nil, false, nil
	}
	shop = m.shops[0]
	set := m.dirty[shop]
	for id := range set {
		if len(ids) == Batch {
			break
		}
		ids = append(ids, id)
		delete(set, id)
	}
	if len(set) == 0 {
		delete(m.dirty, shop)
		m.shops = m.shops[1:]
	}
	return shop, ids, len(ids) > 0, nil
}

// EnqueueFull asks for a full catalog scan of a shop.
func (m *Memory) EnqueueFull(_ context.Context, shop string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.full {
		if s == shop {
			return nil
		}
	}
	m.full = append(m.full, shop)
	return nil
}

// NextFull takes one shop waiting for a full scan.
func (m *Memory) NextFull(_ context.Context) (shop string, ok bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.full) == 0 {
		return "", false, nil
	}
	shop, m.full = m.full[0], m.full[1:]
	return shop, true, nil
}
