package events

// Middleware wraps a [Handler]. sub carries the contract, the consumer
// and the group being wrapped, so one chain can behave differently per
// consumer group; next is the handler to call - never sub.Handle, which
// is the undecorated one.
type Middleware func(sub Subscription, next Handler) Handler

// Chain composes middlewares in outermost-first order: a chain
// `NewChain(A, B, C).Apply(subs)` wraps each subscription as A(B(C(h))),
// so a message flows A → B → C → h and the return travels back in
// reverse.
//
// Chains are value types - Append returns a new chain rather than
// mutating the receiver, so a base chain shared across deployables is
// safe to extend per binary. Nil entries are tolerated and skipped at
// Apply time so optional middlewares can drop into the slice without an
// `if mw != nil` guard at every call site.
//
// This is craftgo's pkg/server.Chain for the consumer side, with one
// difference: the verb is Apply over a slice of subscriptions, not Then
// over one handler. Each wrap needs the [Subscription] it is wrapping, so
// there is nothing to fold onto.
type Chain []Middleware

// NewChain seeds a chain with the supplied middlewares in
// outermost-first order. The result is a fresh slice - mutating mws
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

// Apply returns a copy of subs with every Handle wrapped in the chain.
// The caller's slice is left alone, so the undecorated subscriptions stay
// usable. An empty chain returns subs unchanged.
//
// A subscription carrying no handler is left alone: wrapping it would
// turn an absent handler into a live one that fails on every message.
// [Bus.Register] refuses one outright, so this only arises for a slice
// the bus is not going to see.
//
// Most projects never call this - [WithMiddleware] puts a chain on the
// bus, which covers every subscription registered through it rather than
// the one slice in hand. Apply is for a caller decorating a slice the bus
// is not going to see, or decorating one slice differently from the rest.
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

// wrap folds the chain over h for one subscription. Iteration is reverse
// so the slice reads naturally outermost-first while wrapping builds from
// the innermost slot outwards.
func (c Chain) wrap(sub Subscription, h Handler) Handler {
	for i := len(c) - 1; i >= 0; i-- {
		if c[i] == nil {
			continue
		}
		h = c[i](sub, h)
	}
	return h
}

// Recover turns a panic below it into a [*PanicError], the same error
// [Bus.Start]'s own recover produces.
//
// A chain installed with [WithMiddleware] needs this nowhere: the bus
// already recovers on both sides of it, so a panicking handler arrives at
// that chain as an ordinary error. Recover is for a chain folded by [Apply]
// instead, which the bus wraps from outside as one opaque handler - put it
// at the innermost end and a hand-applied chain sees a panic the way a bus
// chain does.
//
// Placing it does not risk a second [*PanicError]: once this one catches,
// no panic is in flight, so every recover above it returns nil.
func Recover() Middleware {
	return func(sub Subscription, next Handler) Handler {
		if next == nil {
			return nil
		}
		// Unset, not a hand-back: this recover is placed INSIDE a chain,
		// so the middlewares above it still return and still decide.
		return recoverHandler(sub, next, DispositionUnset)
	}
}
