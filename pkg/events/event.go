package events

import (
	"context"
	"errors"
)

// Event is one contract: its subject on the wire, and the validation its
// payload type carries. Generated code declares one per event in the
// design and everything typed about that event goes through it - the
// publish, the decode, the subscription.
//
//	var Created = events.NewEvent[ThemeCreatedEvent](CreatedContract, (*ThemeCreatedEvent).Validate)
//
// A descriptor holds no bus. The bus is passed at the call, so one
// contract catalogue serves every deployable that imports it, whatever
// each is wired to.
type Event[T any] struct {
	contract string
	validate func(*T) error
}

// NewEvent describes the contract published and consumed as payload T.
// validate may be nil, for a payload type with no validation.
func NewEvent[T any](contract string, validate func(*T) error) Event[T] {
	return Event[T]{contract: contract, validate: validate}
}

// Contract is the subject this event travels on, the value that reaches
// [Message.Event].
func (e Event[T]) Contract() string { return e.contract }

// errNoPayload is a publish with nothing to publish. It is a
// [*PayloadError] like a failed validation: the same call fails the same
// way every time.
var errNoPayload = errors.New("no payload")

// Publish validates payload and publishes it through bus. A payload that
// does not validate is a [*PayloadError] and nothing is sent - the
// contract is refused where it is broken rather than at every consumer.
func (e Event[T]) Publish(ctx context.Context, bus *Bus, payload *T, opts ...PublishOption) error {
	if payload == nil {
		return &PayloadError{Event: e.contract, Err: errNoPayload}
	}
	if err := e.validated(payload); err != nil {
		return err
	}
	return bus.Publish(ctx, e.contract, payload, opts...)
}

// Handler adapts a typed handler to the untyped one a transport delivers
// to: the message is decoded with the bus's codec for this contract and
// validated before fn runs.
//
// A payload that cannot be decoded, or that does not validate, is a
// [*PayloadError] - the same bytes fail the same way on every delivery,
// so a chain can pick that out and give the message up rather than retry
// it. A message stamped with another codec is [ErrCodecMismatch] instead,
// which is a configuration mistake and not a poison payload.
//
// bus is a parameter because the codec is the bus's: the descriptor is a
// value in a contract package and knows nothing about how any deployable
// is wired.
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

// Subscription is this event consumed by fn under group. It is one line
// of an application's consumption, handed to [Bus.Register] or
// [Bus.RegisterAll].
//
// There is no consumer parameter and no chain parameter: the middleware
// every handler runs behind belongs on the bus, through [Bus.Use], and
// [Subscription.Consumer] defaults to the contract, which is the name
// [Bus.Plan] and a [*PanicError] then show. A caller who needs either -
// two subscriptions of one contract in one process to tell apart, one
// handler to wrap alone - sets the field on the value before registering
// it:
//
//	sub := orders.Placed.Subscription(bus, Group, l.OrderPlaced)
//	sub.Consumer = "SendReceipt"
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
