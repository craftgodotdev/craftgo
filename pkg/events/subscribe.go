package events

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sort"
)

// PanicError is the error a recovered panic in a handler or its chain becomes; see
// [Bus.Start].
type PanicError struct {
	// Event, Consumer and Group name the subscription that panicked.
	Event    string
	Consumer string
	Group    Group
	// Value is what was passed to panic.
	Value any
	// Stack is the trace captured where the panic fired; Error omits it.
	Stack []byte
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("events: panic in consumer %q (group %q, contract %q): %v",
		e.Consumer, e.Group, e.Event, e.Value)
}

// Unwrap returns the panic value when it is an error, else nil.
func (e *PanicError) Unwrap() error {
	err, _ := e.Value.(error)
	return err
}

// decorated wraps sub's handler in the order [Bus.Start] describes. busChain is the chain
// Start read under the lock; escaped is what the outer recover asks for.
func decorated(busChain Chain, sub Subscription, escaped Disposition) Handler {
	h := recoverHandler(sub, sub.Handle, DispositionUnset)
	chain := busChain.Append(sub.Chain...)
	if len(chain) == 0 {
		return h
	}
	return recoverHandler(sub, chain.wrap(sub, h), escaped)
}

// recoverHandler turns a panic in h into a [*PanicError] naming sub and sets the message's
// disposition to escaped, voiding whatever the panicking frames asked for.
func recoverHandler(sub Subscription, h Handler, escaped Disposition) Handler {
	event, consumer, group := sub.Event, sub.Consumer, sub.Group
	return func(ctx context.Context, msg *Message) (err error) {
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			err = &PanicError{
				Event:    event,
				Consumer: consumer,
				Group:    group,
				Value:    r,
				Stack:    debug.Stack(),
			}
			if msg != nil {
				msg.disposition = escaped
			}
		}()
		return h(ctx, msg)
	}
}

// ErrStarted is returned by a second [Bus.Start], and by [Bus.Register] after one.
var ErrStarted = errors.New("events: the bus has already started")

// ErrNoGroup is returned by [Bus.Register] for a subscription with no group.
var ErrNoGroup = errors.New("events: a subscription needs a group")

// ErrNoHandler is returned by [Bus.Register] for a subscription with no handler.
var ErrNoHandler = errors.New("events: a subscription needs a handler")

// ErrDuplicateSubscription is returned by [Bus.Register] for a contract already
// registered under the same group.
var ErrDuplicateSubscription = errors.New("events: this contract is already registered under this group")

// RegisterError is what [Bus.Register] refuses with: the subscription, and the sentinel
// in Err.
type RegisterError struct {
	Event    string
	Consumer string
	Group    Group
	Err      error
}

func (e *RegisterError) Error() string {
	return fmt.Sprintf("events: register %s/%s in group %q: %v", e.Event, e.Consumer, e.Group, e.Err)
}

func (e *RegisterError) Unwrap() error { return e.Err }

// Use appends mws to the bus chain after construction; see [Bus.Start] for the wrap
// order. Use after [Bus.Start] panics.
func (b *Bus) Use(mws ...Middleware) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started {
		panic("events: Bus.Use called after Bus.Start")
	}
	b.chain = b.chain.Append(mws...)
}

// Register records sub for [Bus.Start]. It refuses with a [*RegisterError] wrapping
// [ErrStarted], [ErrNoHandler], [ErrNoGroup], [ErrNoCodec], [ErrDispositionUnsupported] or
// [ErrDuplicateSubscription]; whether the broker accepts the batch is Start's answer.
func (b *Bus) Register(sub Subscription) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started {
		return registerError(sub, ErrStarted)
	}
	if sub.Handle == nil {
		return registerError(sub, ErrNoHandler)
	}
	if sub.Group == "" {
		return registerError(sub, ErrNoGroup)
	}
	if _, err := b.CodecFor(sub.Event); err != nil {
		return registerError(sub, err)
	}
	if err := b.requireDispositions(); err != nil {
		return registerError(sub, err)
	}
	if b.claimed == nil {
		b.claimed = map[registration]bool{}
	}
	key := registration{event: sub.Event, group: sub.Group}
	if b.claimed[key] {
		return registerError(sub, ErrDuplicateSubscription)
	}
	b.claimed[key] = true
	b.subs = append(b.subs, sub)
	return nil
}

func registerError(sub Subscription, err error) error {
	return &RegisterError{Event: sub.Event, Consumer: sub.Consumer, Group: sub.Group, Err: err}
}

// Start hands every registered subscription to the transport in one call, sorted by
// group, contract then consumer, and wraps each handler as: outer recover → bus chain →
// subscription chain → inner recover → handler. A panic in the chain asks for
// [DispositionRedeliver] where the transport can honour it; a panic in the handler leaves
// the decision to the chain. A second Start is [ErrStarted], even after a failed one.
func (b *Bus) Start(ctx context.Context) error {
	b.mu.Lock()
	if b.started {
		b.mu.Unlock()
		return ErrStarted
	}
	b.started = true
	subs := sortedSubscriptions(b.subs)
	chain := b.chain
	b.mu.Unlock()

	if len(subs) == 0 {
		return nil
	}
	if b.sub == nil {
		return ErrNoSubscriber
	}
	escaped := DispositionUnset
	if canDisposition(b.sub, DispositionRedeliver) {
		escaped = DispositionRedeliver
	}
	for i := range subs {
		subs[i].Handle = decorated(chain, subs[i], escaped)
	}
	if err := b.sub.Subscribe(ctx, subs); err != nil {
		return fmt.Errorf("events: start %d subscription(s): %w", len(subs), err)
	}
	return nil
}

// sortedSubscriptions is a copy of subs in the order the transport receives them.
func sortedSubscriptions(subs []Subscription) []Subscription {
	out := make([]Subscription, len(subs))
	copy(out, subs)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		if out[i].Event != out[j].Event {
			return out[i].Event < out[j].Event
		}
		return out[i].Consumer < out[j].Consumer
	})
	return out
}
