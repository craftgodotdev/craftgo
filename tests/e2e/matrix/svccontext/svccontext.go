package svccontext

import "sync"

// ServiceContext is the matrix fixture's dependency container: the generated
// Middlewares plus the runtime state the server-roundtrip services need.
type ServiceContext struct {
	Middlewares

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
}

// NewServiceContext returns a ServiceContext seeded with deterministic demo
// data so HTTP assertions stay stable.
func NewServiceContext() *ServiceContext {
	return &ServiceContext{
		Users:    map[string]map[string]any{},
		Profiles: map[string]any{},
		OrderID:  "ord-1", OrderTotal: 9900,
		ItemSKU: "sku-1", ItemPrice: 1990,
	}
}

// Lock / Unlock expose the embedded mutex so handlers keep mutations atomic.
func (s *ServiceContext) Lock()   { s.mu.Lock() }
func (s *ServiceContext) Unlock() { s.mu.Unlock() }
