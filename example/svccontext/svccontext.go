// Package svccontext is the user-owned dependency container for the
// Bookstore showcase. The embedded Middlewares struct (codegen-owned,
// next to this file) declares the typed middleware fields; main.go
// assigns each one to a concrete impl from the middleware package.
package svccontext

import (
	"sync"
	"sync/atomic"
)

// ServiceContext aggregates everything a logic / middleware function
// might want at request time. Real projects park database handles,
// secret stores, and the OTel tracer here.
type ServiceContext struct {
	Middlewares

	mu     sync.Mutex
	Books  map[string]Book
	Orders map[string]Order

	bookCounter  atomic.Int64
	orderCounter atomic.Int64
}

// Book is the in-memory shape stored under svc.Books. Wire-level Book
// (in the generated types package) maps 1:1.
type Book struct {
	ID         string
	Title      string
	Author     string
	ISBN       string
	PriceCents int
	Stock      int
}

// Order is the in-memory shape stored under svc.Orders. The wire
// version lives in `internal/types/design/types.go`.
type Order struct {
	ID            string
	CustomerEmail string
	Lines         []OrderLine
	TotalCents    int
	Status        string
}

// OrderLine is one row in an Order.
type OrderLine struct {
	BookID    string
	Quantity  int
	UnitPrice int
}

// NewServiceContext returns a fresh, empty ServiceContext. The caller
// is expected to assign the embedded middleware fields before the
// generated routes register against the server.
func NewServiceContext() *ServiceContext {
	return &ServiceContext{
		Books:  map[string]Book{},
		Orders: map[string]Order{},
	}
}

// Lock / Unlock expose the embedded mutex so logic files can keep
// their per-request mutations atomic.
func (s *ServiceContext) Lock()   { s.mu.Lock() }
func (s *ServiceContext) Unlock() { s.mu.Unlock() }

// NextBookID returns a stable, monotonically-increasing book id.
func (s *ServiceContext) NextBookID() string {
	return "b" + itoa(int(s.bookCounter.Add(1)))
}

// NextOrderID returns a stable, monotonically-increasing order id.
func (s *ServiceContext) NextOrderID() string {
	return "o" + itoa(int(s.orderCounter.Add(1)))
}

// itoa is a small inline integer→string helper so this file doesn't
// pull in `strconv` just for two callers.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = '0' + byte(n%10)
		n /= 10
	}
	return string(buf[pos:])
}
