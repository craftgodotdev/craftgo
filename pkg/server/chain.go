package server

import "net/http"

// Chain is a list of middlewares, outermost first: NewChain(A, B, C).Then(h) is A(B(C(h))).
// Append copies, so a shared chain is safe to extend; Then skips nil entries.
type Chain []Middleware

// NewChain returns a chain holding a copy of mws.
func NewChain(mws ...Middleware) Chain {
	if len(mws) == 0 {
		return nil
	}
	out := make(Chain, len(mws))
	copy(out, mws)
	return out
}

// Append returns c extended by mws at the innermost end, leaving c unchanged.
func (c Chain) Append(mws ...Middleware) Chain {
	if len(mws) == 0 {
		return c
	}
	out := make(Chain, len(c)+len(mws))
	copy(out, c)
	copy(out[len(c):], mws)
	return out
}

// Then wraps h in the chain.
func (c Chain) Then(h http.Handler) http.Handler {
	for i := len(c) - 1; i >= 0; i-- {
		if c[i] == nil {
			continue
		}
		h = c[i](h)
	}
	return h
}

// ThenFunc is [Chain.Then] for a handler function.
func (c Chain) ThenFunc(h http.HandlerFunc) http.Handler {
	return c.Then(h)
}
