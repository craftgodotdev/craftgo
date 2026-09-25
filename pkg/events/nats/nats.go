// Package nats adapts craftgo's event runtime to NATS: [Transport] over core
// NATS and [JetStream] over JetStream streams.
//
// A contract maps onto a subject, the contract name unchanged by default.
// The ordering key and the deduplication ID travel in the [HeaderKey] and
// [HeaderDedupID] headers. A subscription's group is the queue group on
// [Transport] and the durable consumer's name on [JetStream].
//
// [MsgFrom] and [JetStreamMsgFrom] expose the message behind a delivery.
// Decide through [events.Message]; do not ack the raw message.
package nats

import (
	"context"
	"fmt"
	"sync"

	"github.com/nats-io/nats.go"

	events "github.com/craftgodotdev/craftgo/pkg/events"
)

var (
	_ events.Publisher      = (*Transport)(nil)
	_ events.Subscriber     = (*Transport)(nil)
	_ events.BatchPublisher = (*Transport)(nil)
	_ events.OptionAware    = (*Transport)(nil)
)

// Adapter is the name [events.WithAdapterOption] addresses this adapter by.
const Adapter = "nats"

// AdapterName implements [events.OptionAware].
func (t *Transport) AdapterName() string { return Adapter }

// KnownOptions implements [events.OptionAware]. This adapter reads no
// per-message options, so any option addressed to it fails the publish.
func (t *Transport) KnownOptions() []string { return nil }

// Transport publishes and subscribes over a NATS connection.
type Transport struct {
	conn    *nats.Conn
	subject func(contract string) string
	onError func(sub events.Subscription, msg *events.Message, err error)

	mu     sync.Mutex
	closed bool
	subs   []*nats.Subscription
}

// Option configures a Transport.
type Option func(*Transport)

// WithSubject replaces the contract-to-subject mapping; the default is the
// contract name unchanged. The mapping must be one-to-one: the contract
// travels only in the subject.
func WithSubject(fn func(contract string) string) Option {
	return func(t *Transport) { t.subject = fn }
}

// WithErrorHandler installs a callback for a handler that returns an error.
// Core NATS delivers at most once, so it is the only record of the failure.
func WithErrorHandler(fn func(sub events.Subscription, msg *events.Message, err error)) Option {
	return func(t *Transport) { t.onError = fn }
}

// New binds a Transport to an existing connection, which the caller owns and
// [Transport.Close] leaves open.
func New(conn *nats.Conn, opts ...Option) *Transport {
	t := &Transport{conn: conn, subject: func(c string) string { return c }}
	for _, o := range opts {
		o(t)
	}
	return t
}

// Publish sends one message. A nil error means it reached the connection's
// buffer, not that a subscriber received it. After [Transport.Close] it
// returns [ErrClosed].
func (t *Transport) Publish(_ context.Context, msg *events.Message) error {
	if t.isClosed() {
		return fmt.Errorf("nats: publish %s: %w", msg.Event, ErrClosed)
	}
	return t.conn.PublishMsg(t.encode(msg))
}

// PublishBatch sends the whole batch, then flushes once. After
// [Transport.Close] it returns [ErrClosed].
func (t *Transport) PublishBatch(ctx context.Context, msgs []*events.Message) error {
	if t.isClosed() {
		return fmt.Errorf("nats: publish batch of %d: %w", len(msgs), ErrClosed)
	}
	for i, msg := range msgs {
		if err := t.conn.PublishMsg(t.encode(msg)); err != nil {
			return events.UnsentFrom(i, msgs, err)
		}
	}
	return t.flush(ctx)
}

// flush waits for the server to take the buffered publishes. FlushWithContext
// refuses a ctx without a deadline; such a ctx uses the connection's timeout.
func (t *Transport) flush(ctx context.Context) error {
	if _, ok := ctx.Deadline(); ok {
		return t.conn.FlushWithContext(ctx)
	}
	return t.conn.Flush()
}

func (t *Transport) encode(msg *events.Message) *nats.Msg {
	return encodeTo(t.subject(msg.Event), msg)
}

// encodeTo is the wire format both transports share. Metadata never
// overrides [HeaderKey] or [HeaderDedupID].
func encodeTo(subject string, msg *events.Message) *nats.Msg {
	out := &nats.Msg{
		Subject: subject,
		Data:    msg.Payload,
		Header:  nats.Header{},
	}
	for k, v := range msg.Metadata {
		if k == HeaderKey || k == HeaderDedupID {
			continue
		}
		out.Header.Set(k, v)
	}
	if msg.Key != "" {
		out.Header.Set(HeaderKey, msg.Key)
	}
	if msg.DedupID != "" {
		out.Header.Set(HeaderDedupID, msg.DedupID)
	}
	return out
}

// HeaderKey carries [events.Message.Key] across the wire.
const HeaderKey = "Craftgo-Key"

// HeaderDedupID carries [events.Message.DedupID] under JetStream's own name,
// so a stream's duplicate window drops a repeat; core NATS only carries it.
const HeaderDedupID = "Nats-Msg-Id"

// Subscribe registers each subscription as a queue subscriber under its
// group, delivering until ctx is cancelled. The first failure stops the
// loop; the subscriptions already made stay live. After [Transport.Close]
// it returns [ErrClosed].
func (t *Transport) Subscribe(ctx context.Context, subs []events.Subscription) error {
	for _, sub := range subs {
		if err := t.subscribeOne(ctx, sub); err != nil {
			return err
		}
	}
	return nil
}

func (t *Transport) subscribeOne(ctx context.Context, sub events.Subscription) error {
	subject := t.subject(sub.Event)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return fmt.Errorf("nats: subscribe %s: %w", subject, ErrClosed)
	}
	s, err := t.conn.QueueSubscribe(subject, string(sub.Group), func(m *nats.Msg) {
		msg := decode(sub.Event, m)
		if err := sub.Handle(withMsg(ctx, m), msg); err != nil && t.onError != nil {
			t.onError(sub, msg, err)
		}
	})
	if err != nil {
		return fmt.Errorf("nats: subscribe %s: %w", subject, err)
	}
	t.subs = append(t.subs, s)
	context.AfterFunc(ctx, func() { _ = s.Unsubscribe() })
	return nil
}

// decode takes the contract from the subscription, so a subject mapping
// need not be reversible.
func decode(contract string, m *nats.Msg) *events.Message {
	return decodeFrom(contract, m.Header, m.Data)
}

// decodeFrom is the reverse of encodeTo. It takes the parts because a
// JetStream delivery is not a *nats.Msg.
func decodeFrom(contract string, header nats.Header, data []byte) *events.Message {
	out := &events.Message{
		Event:    contract,
		Key:      header.Get(HeaderKey),
		DedupID:  header.Get(HeaderDedupID),
		Payload:  data,
		Metadata: map[string]string{},
	}
	for k := range header {
		if k == HeaderKey || k == HeaderDedupID {
			continue
		}
		out.Metadata[k] = header.Get(k)
	}
	return out
}

// Close unsubscribes everything this transport registered; a publish or
// subscribe after it returns [ErrClosed]. The connection is left open.
func (t *Transport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	for _, s := range t.subs {
		_ = s.Unsubscribe()
	}
	t.subs = nil
	return nil
}

func (t *Transport) isClosed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}
