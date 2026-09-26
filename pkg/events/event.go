package events

import (
	"context"
	"errors"
)

// Event is one contract: its name on the wire and its payload type's validation.
// Generated code declares one per event; the bus is passed at each call.
type Event[T any] struct {
	contract string
	validate func(*T) error
}

// NewEvent describes the contract published and consumed as payload T; validate may be
// nil.
func NewEvent[T any](contract string, validate func(*T) error) Event[T] {
	return Event[T]{contract: contract, validate: validate}
}

// Contract is the name this event travels under, the value of [Message.Event].
func (e Event[T]) Contract() string { return e.contract }

// errNoPayload is the cause of the [*PayloadError] a nil payload gets.
var errNoPayload = errors.New("no payload")

// Publish validates payload and publishes it through bus with [Bus.Publish]. A nil or
// invalid payload is a [*PayloadError] and nothing is sent.
func (e Event[T]) Publish(ctx context.Context, bus *Bus, payload *T, opts ...PublishOption) error {
	if payload == nil {
		return &PayloadError{Event: e.contract, Err: errNoPayload}
	}
	if err := e.validated(payload); err != nil {
		return err
	}
	return bus.Publish(ctx, e.contract, payload, opts...)
}

// Handler adapts fn to a [Handler] that decodes the message with bus's codec and
// validates it before fn runs. An undecodable or invalid payload is a [*PayloadError];
// a message stamped with another codec is [ErrCodecMismatch].
func (e Event[T]) Handler(bus *Bus, fn func(ctx context.Context, payload *T) error) Handler {
	return func(ctx context.Context, msg *Message) error {
		var payload T
		if err := bus.Decode(msg, &payload); err != nil {
			return err
		}
		if err := e.validated(&payload); err != nil {
			return err
		}
		return fn(ctx, &payload)
	}
}

// Subscribe registers fn for this event under group on bus; see [Bus.Register]. Nothing
// is delivered until [Bus.Start].
func (e Event[T]) Subscribe(bus *Bus, group Group, fn func(ctx context.Context, payload *T) error) error {
	return bus.Register(e.Subscription(bus, group, fn))
}

// Subscription is this event consumed by fn under group, as the value [Bus.Register]
// takes; its Consumer is the contract name and its Chain is empty.
func (e Event[T]) Subscription(bus *Bus, group Group, fn func(ctx context.Context, payload *T) error) Subscription {
	return Subscription{
		Event:    e.contract,
		Consumer: e.contract,
		Group:    group,
		Handle:   e.Handler(bus, fn),
	}
}

// validated runs the payload type's own validation, if it has any.
func (e Event[T]) validated(payload *T) error {
	if e.validate == nil {
		return nil
	}
	if err := e.validate(payload); err != nil {
		return &PayloadError{Event: e.contract, Err: err}
	}
	return nil
}
