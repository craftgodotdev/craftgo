// Package nats adapts craftgo's event runtime to NATS.
//
// A contract maps onto a subject. Contract names are already dot-shaped
// (`orders.OrderPlaced`), which is the shape NATS subjects want, so the
// default mapping is one-to-one and a wildcard subscription like
// `orders.>` keeps working. Pass [WithSubject] when the broker's naming
// is not yours to choose.
//
// A subscription's group is the queue group, so replicas sharing one
// group share the work while a different group gets its own copy - the
// same competing-consumer model the in-process transport implements.
package nats

import (
	"context"
	"fmt"
	"sync"

	"github.com/nats-io/nats.go"

	events "github.com/craftgodotdev/craftgo/pkg/events"
)

// A Transport is a full transport: it publishes, subscribes, takes a
// batch in one call, and names itself to the per-message option check.
// Asserted here so a change to the runtime interfaces fails this package
// rather than a user's wiring.
var (
	_ events.Publisher      = (*Transport)(nil)
	_ events.Subscriber     = (*Transport)(nil)
	_ events.BatchPublisher = (*Transport)(nil)
	_ events.OptionAware    = (*Transport)(nil)
)

// Adapter is the name [events.WithAdapterOption] addresses this adapter
// by.
const Adapter = "nats"

// AdapterName implements [events.OptionAware].
func (t *Transport) AdapterName() string { return Adapter }

// KnownOptions implements [events.OptionAware]. This adapter reads no
// per-message options: a subject carries no per-message settings, and the
// two values that would be them - the ordering key and the deduplication
// ID - are [events.WithKey] and [events.WithDedupID], which every
// transport takes. So an option addressed to `nats` is always a mistake,
// and saying so is better than dropping it.
func (t *Transport) KnownOptions() []string { return nil }

// Transport publishes and subscribes over a NATS connection.
type Transport struct {
	conn    *nats.Conn
	subject func(contract string) string
	onError func(sub events.Subscription, msg *events.Message, err error)

	mu   sync.Mutex
	subs []*nats.Subscription
}

// Option configures a Transport.
type Option func(*Transport)

// WithSubject replaces the contract-to-subject mapping. The default is
// the contract name unchanged.
//
// The mapping must be one-to-one. The contract travels in the subject and
// nowhere else, so two contracts sharing a subject reach each other's
// subscriptions, each delivery labelled with whichever contract the
// subscription asked for.
func WithSubject(fn func(contract string) string) Option {
	return func(t *Transport) { t.subject = fn }
}

// WithErrorHandler installs a callback for a handler that returns an
// error. NATS core delivers at most once and has no nack, so without a
// handler a failed message is observed by nothing.
func WithErrorHandler(fn func(sub events.Subscription, msg *events.Message, err error)) Option {
	return func(t *Transport) { t.onError = fn }
}

// New binds a Transport to an existing connection. The caller owns the
// connection's lifetime; [Transport.Close] only drains this transport's
// subscriptions.
func New(conn *nats.Conn, opts ...Option) *Transport {
	t := &Transport{conn: conn, subject: func(c string) string { return c }}
	for _, o := range opts {
		o(t)
	}
	return t
}

// Publish sends one message. NATS core is fire-and-forget: a nil error
// means the bytes reached the connection's buffer, not that a subscriber
// received them.
func (t *Transport) Publish(_ context.Context, msg *events.Message) error {
	return t.conn.PublishMsg(t.encode(msg))
}

// PublishBatch sends the whole batch, then flushes once instead of per
// message.
func (t *Transport) PublishBatch(ctx context.Context, msgs []*events.Message) error {
	for i, msg := range msgs {
		if err := t.conn.PublishMsg(t.encode(msg)); err != nil {
			return events.UnsentFrom(i, msgs, err)
		}
	}
	return t.flush(ctx)
}

// flush waits for the server to acknowledge the buffered publishes.
// FlushWithContext rejects a context with no deadline, and the usual
// caller context has none, so fall back to the connection's own timeout
// rather than failing a batch for want of a deadline.
func (t *Transport) flush(ctx context.Context) error {
	if _, ok := ctx.Deadline(); ok {
		return t.conn.FlushWithContext(ctx)
	}
	return t.conn.Flush()
}

// encode maps a craftgo message onto a NATS message. The contract rides
// the subject; the ordering key, a deduplication ID and the codec stamp
// ride headers, so a consumer can read them without decoding the payload.
//
// [HeaderKey] and [HeaderDedupID] are this adapter's, so a metadata entry
// under either name is skipped: decode reads the ordering key back out of
// one, and a message published without a key would otherwise arrive
// carrying the caller's value as one.
func (t *Transport) encode(msg *events.Message) *nats.Msg {
	out := &nats.Msg{
		Subject: t.subject(msg.Event),
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

// HeaderDedupID carries [events.Message.DedupID]. The name is JetStream's
// own: a subject backed by a stream with a duplicate window takes one of
// two publishes sharing this value, and core NATS carries the header to
// whoever is listening without acting on it.
const HeaderDedupID = "Nats-Msg-Id"

// Subscribe registers sub as a queue subscriber under its group.
// Delivery runs until ctx is cancelled.
func (t *Transport) Subscribe(ctx context.Context, sub events.Subscription) error {
	subject := t.subject(sub.Event)
	s, err := t.conn.QueueSubscribe(subject, sub.GroupName(), func(m *nats.Msg) {
		msg := decode(sub.Event, m)
		if err := sub.Handle(ctx, msg); err != nil && t.onError != nil {
			t.onError(sub, msg, err)
		}
	})
	if err != nil {
		return fmt.Errorf("nats: subscribe %s: %w", subject, err)
	}
	t.mu.Lock()
	t.subs = append(t.subs, s)
	t.mu.Unlock()
	go func() {
		<-ctx.Done()
		_ = s.Unsubscribe()
	}()
	return nil
}

// decode rebuilds a craftgo message from a NATS delivery. The contract
// comes from the subscription rather than the subject, so a custom
// subject mapping does not have to be reversible.
//
// [HeaderKey] is consumed into Key rather than left in Metadata, so a
// consumer sees the same entries here as on any other transport.
func decode(contract string, m *nats.Msg) *events.Message {
	out := &events.Message{
		Event:    contract,
		Key:      m.Header.Get(HeaderKey),
		DedupID:  m.Header.Get(HeaderDedupID),
		Payload:  m.Data,
		Metadata: map[string]string{},
	}
	for k := range m.Header {
		if k == HeaderKey || k == HeaderDedupID {
			continue
		}
		out.Metadata[k] = m.Header.Get(k)
	}
	return out
}

// Close unsubscribes everything this transport registered. The
// connection is left open.
func (t *Transport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, s := range t.subs {
		_ = s.Unsubscribe()
	}
	t.subs = nil
	return nil
}
