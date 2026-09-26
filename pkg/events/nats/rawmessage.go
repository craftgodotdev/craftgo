package nats

import (
	"context"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// msgKey is the context key for a [Transport] delivery's message.
type msgKey struct{}

// MsgFrom returns the message behind a [Transport] delivery, if ctx is one.
func MsgFrom(ctx context.Context) (*nats.Msg, bool) {
	m, ok := ctx.Value(msgKey{}).(*nats.Msg)
	return m, ok
}

// MustMsg is like [MsgFrom] but panics if ctx carries no message.
func MustMsg(ctx context.Context) *nats.Msg {
	m, ok := MsgFrom(ctx)
	if !ok {
		panic("nats: no NATS message on this context - this middleware is installed on a transport that is not nats")
	}
	return m
}

func withMsg(ctx context.Context, m *nats.Msg) context.Context {
	return context.WithValue(ctx, msgKey{}, m)
}

// jetStreamMsgKey is the context key for a [JetStream] delivery's message.
type jetStreamMsgKey struct{}

// JetStreamMsgFrom returns the message behind a [JetStream] delivery, if any.
func JetStreamMsgFrom(ctx context.Context) (jetstream.Msg, bool) {
	m, ok := ctx.Value(jetStreamMsgKey{}).(jetstream.Msg)
	return m, ok
}

// MustJetStreamMsg is like [JetStreamMsgFrom] but panics if ctx carries none.
func MustJetStreamMsg(ctx context.Context) jetstream.Msg {
	m, ok := JetStreamMsgFrom(ctx)
	if !ok {
		panic("nats: no JetStream message on this context - this middleware is installed on a transport that is not JetStream")
	}
	return m
}

func withJetStreamMsg(ctx context.Context, m jetstream.Msg) context.Context {
	return context.WithValue(ctx, jetStreamMsgKey{}, m)
}
