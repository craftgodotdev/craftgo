package svccontext

import "sync"

// ServiceContext is the notifier deployable's dependency container.
// `output.main: "-"` leaves this file to the project; the generated
// Middlewares and Events beside it are craftgo's.
type ServiceContext struct {
	Middlewares
	Events Events

	mu sync.Mutex
	// Delivered records every payload the consumers handled, keyed by
	// consumer name, so the projection's round-trip test can assert on
	// what each one saw.
	Delivered map[string][]any
}

// NewServiceContext returns an empty container ready for the generated
// consumers to write into.
func NewServiceContext() *ServiceContext {
	return &ServiceContext{Delivered: map[string][]any{}}
}

// Record stores one delivered payload under the consumer that handled it.
func (s *ServiceContext) Record(consumer string, payload any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Delivered[consumer] = append(s.Delivered[consumer], payload)
}

// DeliveredTo returns the payloads one consumer handled.
func (s *ServiceContext) DeliveredTo(consumer string) []any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]any(nil), s.Delivered[consumer]...)
}
