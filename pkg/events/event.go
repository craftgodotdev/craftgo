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

// Item is one message of a batch: its payload, decoded and validated, and the delivery,
// whose Redeliver or Reject answers for this message alone.
type Item[T any] struct {
	Payload *T
	Msg     *Message
	err     *error
}

// Fail records err as this item's own failure, which the batch function's error does not
// replace; nil clears it.
func (it Item[T]) Fail(err error) {
	if it.err != nil {
		*it.err = err
	}
}

// BatchHandler adapts fn to a [BatchHandler] that decodes and validates each message as
// [Event.Handler] does. A message that fails is left out of fn's batch, and fn does not
// run for a batch with none left; fn's error goes to each item not failed with
// [Item.Fail]. The failures come back as [ItemErrors], by index into the whole batch.
func (e Event[T]) BatchHandler(bus *Bus, fn func(ctx context.Context, batch []Item[T]) error) BatchHandler {
	return func(ctx context.Context, msgs []*Message) error {
		errs := make([]error, len(msgs))
		items := make([]Item[T], 0, len(msgs))
		for i, msg := range msgs {
			var payload T
			if err := bus.Decode(msg, &payload); err != nil {
				errs[i] = err
				continue
			}
			if err := e.validated(&payload); err != nil {
				errs[i] = err
				continue
			}
			items = append(items, Item[T]{Payload: &payload, Msg: msg, err: &errs[i]})
		}
		if len(items) > 0 {
			if err := fn(ctx, items); err != nil {
				for _, it := range items {
					if *it.err == nil {
						*it.err = err
					}
				}
			}
		}
		return itemErrors(errs)
	}
}

// SubscribeBatch registers fn for batches of this event, bounded by size, under group;
// see [Bus.Register]. Nothing is delivered until [Bus.Start].
func (e Event[T]) SubscribeBatch(bus *Bus, group Group, size BatchSize, fn func(ctx context.Context, batch []Item[T]) error) error {
	return bus.Register(e.BatchSubscription(bus, group, size, fn))
}

// BatchSubscription is this event consumed in batches bounded by size by fn under group,
// as the value [Bus.Register] takes; its Consumer is the contract name and its batch
// chain is empty.
func (e Event[T]) BatchSubscription(bus *Bus, group Group, size BatchSize, fn func(ctx context.Context, batch []Item[T]) error) Subscription {
	return Subscription{
		Event:    e.contract,
		Consumer: e.contract,
		Group:    group,
		Batch:    &Batch{BatchSize: size, Handle: e.BatchHandler(bus, fn)},
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
