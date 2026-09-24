package events

import (
	"errors"
	"fmt"
)

// Disposition is what the chain asked for one delivery. The last of [Message.Settle],
// [Message.Redeliver] and [Message.Reject] called before the chain returns wins, so the
// outermost middleware decides last; call them only from the delivery's goroutine.
type Disposition uint8

const (
	// DispositionUnset is the zero value: nothing was asked.
	DispositionUnset Disposition = iota
	// DispositionSettle takes the delivery as done; every transport honours it.
	DispositionSettle
	// DispositionRedeliver hands the message back for another attempt.
	DispositionRedeliver
	// DispositionReject gives the message up as one no attempt will handle.
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
func (m *Message) Settle() { m.disposition = DispositionSettle }

// Redeliver asks for this message to come back. A transport that cannot honour it settles
// instead; [WithDispositionRequired] makes [Bus.Register] refuse such a transport.
func (m *Message) Redeliver() { m.disposition = DispositionRedeliver }

// Reject gives this message up. A transport that cannot honour it settles instead.
func (m *Message) Reject() { m.disposition = DispositionReject }

// Disposition returns what has been asked for this delivery.
func (m *Message) Disposition() Disposition { return m.disposition }

// Deliveries is how many times the broker has handed this message over, this one
// included; zero when the transport does not count.
func (m *Message) Deliveries() int { return m.deliveries }

// SetDeliveries records the broker's delivery count; a transport adapter calls it while
// decoding.
func (m *Message) SetDeliveries(n int) { m.deliveries = n }

// Dispositioner is the optional upgrade for a transport that can do more with a delivery
// than settle it. It is asked per instance, and its answer must not change after
// construction. A transport without it honours [DispositionSettle] alone.
type Dispositioner interface {
	CanDisposition(d Disposition) bool
}

// ErrDispositionUnsupported is returned by [Bus.Register] when the transport cannot
// honour a disposition [WithDispositionRequired] named.
var ErrDispositionUnsupported = errors.New("events: transport cannot honour a required disposition")

// WithDispositionRequired makes [Bus.Register] refuse a transport that cannot honour d,
// with [ErrDispositionUnsupported]. Repeated calls accumulate.
func WithDispositionRequired(d Disposition) Option {
	return func(b *Bus) { b.required = append(b.required, d) }
}

// requireDispositions checks [WithDispositionRequired] against the subscribe half.
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
