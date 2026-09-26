package events

// Middleware wraps next, the handler for sub. It continues by calling next, never
// sub.Handle, which is undecorated.
type Middleware func(sub Subscription, next Handler) Handler

// Chain is a list of middlewares, outermost first: NewChain(A, B, C) wraps a handler as
// A(B(C(h))). Append returns a new chain, and nil entries are skipped.
type Chain []Middleware

// NewChain returns a chain of mws, outermost first, copied from mws.
func NewChain(mws ...Middleware) Chain {
	if len(mws) == 0 {
		return nil
	}
	out := make(Chain, len(mws))
	copy(out, mws)
	return out
}

// Append returns a new chain with mws added at the innermost end; the receiver is
// unchanged.
func (c Chain) Append(mws ...Middleware) Chain {
	if len(mws) == 0 {
		return c
	}
	out := make(Chain, len(c)+len(mws))
	copy(out, c)
	copy(out[len(c):], mws)
	return out
}

// Apply returns a copy of subs with every Handle wrapped in the chain, leaving subs and
// any subscription with no handler untouched. An empty chain returns subs itself.
func (c Chain) Apply(subs []Subscription) []Subscription {
	if len(c) == 0 {
		return subs
	}
	out := make([]Subscription, len(subs))
	copy(out, subs)
	for i, sub := range out {
		if sub.Handle == nil {
			continue
		}
		out[i].Handle = c.wrap(sub, sub.Handle)
	}
	return out
}

// wrap folds the chain over h, from the innermost entry outwards.
func (c Chain) wrap(sub Subscription, h Handler) Handler {
	for i := len(c) - 1; i >= 0; i-- {
		if c[i] == nil {
			continue
		}
		h = c[i](sub, h)
	}
	return h
}

// Recover turns a panic below it into a [*PanicError], resetting the disposition to unset.
// Put it innermost in a chain folded by [Chain.Apply], or beneath a middleware that must
// see a panic from the middleware below it; a bus chain already sees a handler's panic.
func Recover() Middleware {
	return func(sub Subscription, next Handler) Handler {
		if next == nil {
			return nil
		}
		// The middlewares above still return and decide.
		return recoverHandler(sub, next, DispositionUnset)
	}
}
