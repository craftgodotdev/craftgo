package server

import "net/http"

// Chain composes middlewares in outermost-first order: a chain
// `NewChain(A, B, C).Then(h)` yields `A(B(C(h)))`, so a request flows
// A → B → C → h and the response leaves in reverse.
//
// Chains are value types — Append returns a new chain rather than
// mutating the receiver, so a base chain shared across routes is safe
// to extend per call site. Nil entries are tolerated and skipped at
// Then time so optional middlewares can drop into the slice without
// an `if mw != nil` guard at every call site.
type Chain []Middleware

// NewChain seeds a chain with the supplied middlewares in
// outermost-first order. The result is a fresh slice — mutating mws
// after the call does not affect the chain.
func NewChain(mws ...Middleware) Chain {
	if len(mws) == 0 {
		return nil
	}
	out := make(Chain, len(mws))
	copy(out, mws)
	return out
}

// Append returns a new chain with mws added at the innermost end.
// The receiver is unchanged.
func (c Chain) Append(mws ...Middleware) Chain {
	if len(mws) == 0 {
		return c
	}
	out := make(Chain, len(c)+len(mws))
	copy(out, c)
	copy(out[len(c):], mws)
	return out
}

// Then folds the chain over h, producing the wrapped handler.
// Iteration is reverse so the slice reads naturally outermost-first
// while wrapping builds from the innermost slot outwards.
func (c Chain) Then(h http.Handler) http.Handler {
	for i := len(c) - 1; i >= 0; i-- {
		if c[i] == nil {
			continue
		}
		h = c[i](h)
	}
	return h
}

// ThenFunc is Then for http.HandlerFunc — saves a cast at the call
// site when the innermost handler is a bare function.
func (c Chain) ThenFunc(h http.HandlerFunc) http.Handler {
	return c.Then(h)
}
