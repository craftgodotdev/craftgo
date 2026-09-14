package nats

import (
	"context"

	"github.com/nats-io/nats.go"
)

// msgKey is this adapter's context key for the message behind a delivery.
// It is UNEXPORTED and its type is distinct from every other adapter's,
// so a NATS-typed read on another transport's delivery cannot find
// anything - it is not that the lookup misses, it is that no value of
// this type was ever stored.
type msgKey struct{}

// MsgFrom returns the NATS message behind the delivery ctx belongs to,
// for a middleware that needs what [events.Message] does not carry: the
// reply subject, the subject a wildcard subscription actually matched, a
// header this adapter did not map.
//
// On a JetStream-backed subject it is also the way to the stream
// metadata - `m.Metadata()` carries the sequence and the delivery count -
// because a JetStream delivery is still a [nats.Msg].
//
// It reports false on a delivery from any other transport, and on a
// context that is not a delivery at all.
//
// Read what you need and let it go rather than keeping the message past
// the handler's return, and decide through [events.Message] - Settle,
// Redeliver, Reject - rather than acking the message yourself: the
// disposition is answered for at the right moment whatever the transport,
// and an ack this adapter did not make is one it cannot account for.
func MsgFrom(ctx context.Context) (*nats.Msg, bool) {
	m, ok := ctx.Value(msgKey{}).(*nats.Msg)
	return m, ok
}

// MustMsg is [MsgFrom] for a middleware that only ever runs on this
// transport. It panics when there is no message, which the bus turns into
// a [events.PanicError] naming the consumer, the group and the contract -
// so a middleware installed on the wrong transport says so on its FIRST
// message rather than quietly doing nothing.
func MustMsg(ctx context.Context) *nats.Msg {
	m, ok := MsgFrom(ctx)
	if !ok {
		panic("nats: no NATS message on this context - this middleware is installed on a transport that is not nats")
	}
	return m
}

// withMsg returns ctx carrying m, for the delivery of that message.
func withMsg(ctx context.Context, m *nats.Msg) context.Context {
	return context.WithValue(ctx, msgKey{}, m)
}
