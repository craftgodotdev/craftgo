package events

import (
	"errors"
	"fmt"
)

// Disposition is what has been asked for one delivery.
//
// The names are craftgo's own rather than any one broker's: franz-go's
// share client calls them AckAccept / AckRelease / AckReject, JetStream
// Ack / Nak / Term, AMQP 1.0 accepted / released / rejected.
type Disposition uint8

const (
	// DispositionUnset is the zero value: no middleware decided.
	DispositionUnset Disposition = iota
	// DispositionSettle takes the delivery as done. Every transport can
	// do this; most can do nothing else.
	DispositionSettle
	// DispositionRedeliver hands the message back for another attempt at
	// the broker's discretion. The same message returns.
	DispositionRedeliver
	// DispositionReject gives the message up as one no attempt will
	// handle. Where the broker puts it is the broker's business.
	DispositionReject
)

func (d Disposition) String() string {
	switch d {
	case DispositionSettle:
		return "settle"
	case DispositionRedeliver:
		return "redeliver"
	case DispositionReject:
		return "reject"
	}
	return "unset"
}

// Settle asks for this delivery to be taken as done.
//
// The last writer wins and clearing is allowed: the chain returns
// innermost first, so the last to decide is the outermost middleware -
// the one listed first at the wiring, which can see what everything below
// asked for. Decide from the handler's goroutine and before the chain
// returns; a decision written from a goroutine of your own is both a race
// and a lost write.
func (m *Message) Settle() { m.disposition = DispositionSettle }

// Redeliver asks for this message to come back. A transport that cannot
// ([Dispositioner]) settles instead, so declare the need at the bus with
// [WithDispositionRequired] and find out at startup. See [Message.Settle]
// for when a decision may be written.
//
// Whether another attempt can ever succeed is the chain's to work out, and
// the error the handler returned is all it has to work from. A payload the
// generated wrapper could not decode or validate comes back as an error
// naming the contract it arrived on, but no type separates one of those
// from a failure in the handler's own logic - so a chain that needs the
// distinction makes it on its own side, by returning an error type of its
// own from the handler and reading it back with [errors.As].
func (m *Message) Redeliver() { m.disposition = DispositionRedeliver }

// Reject gives this message up. See [Message.Redeliver] for what a
// transport that cannot does, and [Message.Settle] for when a decision
// may be written.
func (m *Message) Reject() { m.disposition = DispositionReject }

// Disposition returns what has been asked for this delivery, which a
// transport reads once the chain has returned.
func (m *Message) Disposition() Disposition { return m.disposition }

// Deliveries is how many times the broker has handed this message over,
// this one included. Zero means the transport does not count.
func (m *Message) Deliveries() int { return m.deliveries }

// SetDeliveries records the broker's delivery count. A transport adapter
// calls it while decoding; nothing in a chain can forge one.
func (m *Message) SetDeliveries(n int) { m.deliveries = n }

// Dispositioner is the optional upgrade for a transport that can do more
// with a delivery than take it as done. It is asked per INSTANCE, not per
// type: one adapter may be built in a mode that can redeliver and in a
// mode that cannot.
//
// A transport that does not implement it honours [DispositionSettle] and
// nothing else, so an adapter with no such mode needs no code here and
// has no answer that can go stale.
//
// The answer must not change after construction. [Bus.Subscribe] reads it
// once per subscription, so one that moved would make Redeliver work on
// one message and not the next with nothing to notice it.
type Dispositioner interface {
	CanDisposition(d Disposition) bool
}

// ErrDispositionUnsupported is returned by [Bus.Subscribe] when the
// transport cannot honour a disposition [WithDispositionRequired] named.
var ErrDispositionUnsupported = errors.New("events: transport cannot honour a required disposition")

// WithDispositionRequired refuses to subscribe on a transport that cannot
// honour d. Repeated calls accumulate.
//
// It is the difference between a delivery guarantee the design states and
// one it hopes for: a chain that calls Redeliver on a transport that
// settles instead loses every message it meant to retry, silently. The
// refusal reaches a boot failure through the generated SubscribeAll.
func WithDispositionRequired(d Disposition) Option {
	return func(b *Bus) { b.required = append(b.required, d) }
}

// requireDispositions applies [WithDispositionRequired] to the subscribe
// half. The capability is read here rather than per message, so it is
// fixed for the life of the subscription.
func (b *Bus) requireDispositions() error {
	for _, d := range b.required {
		if !canDisposition(b.sub, d) {
			return fmt.Errorf("%w: %s", ErrDispositionUnsupported, d)
		}
	}
	return nil
}

// canDisposition reports whether sub honours d.
func canDisposition(sub Subscriber, d Disposition) bool {
	if dp, ok := sub.(Dispositioner); ok {
		return dp.CanDisposition(d)
	}
	return d == DispositionSettle
}
