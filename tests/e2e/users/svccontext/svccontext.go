package svccontext

import "sync"

// ServiceContext is the user-owned dependency container. Each fixture
// test resets the in-memory store before exercising the API.
type ServiceContext struct {
	mu    sync.Mutex
	Users map[string]map[string]any
}

// NewServiceContext returns an empty in-memory ServiceContext.
func NewServiceContext() *ServiceContext {
	return &ServiceContext{Users: map[string]map[string]any{}}
}

// Lock / Unlock expose the embedded mutex so the logic layer can keep its
// per-request mutations atomic.
func (s *ServiceContext) Lock()   { s.mu.Lock() }
func (s *ServiceContext) Unlock() { s.mu.Unlock() }
