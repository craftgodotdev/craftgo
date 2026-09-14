package svccontext

import "sync"

// ServiceContext is the matrix fixture's dependency container: the generated
// Middlewares and Events plus the runtime state the server-roundtrip services
// need.
type ServiceContext struct {
	Middlewares
	Events Events

	mu sync.Mutex

	// account-user service - in-memory store.
	Users map[string]map[string]any

	// profile service - in-memory store + id allocator.
	Profiles map[string]any
	NextID   int

	// runtime services (orders / catalog) - seeded demo values.
	OrderID    string
	OrderTotal int
	ItemSKU    string
	ItemPrice  int

	// Delivered records every payload the event consumers handled, keyed
	// by consumer name, so the event round-trip test can assert on what
	// each one saw.
	Delivered map[string][]any
}

// NewServiceContext returns a ServiceContext seeded with deterministic demo
// data so HTTP assertions stay stable.
func NewServiceContext() *ServiceContext {
	return &ServiceContext{
		Users:    map[string]map[string]any{},
		Profiles: map[string]any{},
		OrderID:  "ord-1", OrderTotal: 9900,
		ItemSKU: "sku-1", ItemPrice: 1990,
		Delivered: map[string][]any{},
	}
}

// Lock / Unlock expose the embedded mutex so handlers keep mutations atomic.
func (s *ServiceContext) Lock()   { s.mu.Lock() }
func (s *ServiceContext) Unlock() { s.mu.Unlock() }

// Record stores one delivered event payload under its consumer name.
func (s *ServiceContext) Record(consumer string, payload any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Delivered[consumer] = append(s.Delivered[consumer], payload)
}

// DeliveredTo returns the payloads one consumer handled.
func (s *ServiceContext) DeliveredTo(consumer string) []any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]any, len(s.Delivered[consumer]))
	copy(out, s.Delivered[consumer])
	return out
}
