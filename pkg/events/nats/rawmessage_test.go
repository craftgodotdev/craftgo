package nats

import (
	"context"
	"strings"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// MsgFrom returns the message a delivery context carries.
func TestTheMessageIsReachableFromADelivery(t *testing.T) {
	m := &nats.Msg{Subject: "orders.Placed", Reply: "_INBOX.1", Data: []byte("body"), Header: nats.Header{}}
	m.Header.Set("x-unmapped", "kept")

	got, ok := MsgFrom(withMsg(context.Background(), m))
	if !ok {
		t.Fatal("MsgFrom found no message on a NATS delivery")
	}
	if got.Subject != "orders.Placed" || got.Reply != "_INBOX.1" {
		t.Errorf("message = subject %q reply %q", got.Subject, got.Reply)
	}
	if got.Header.Get("x-unmapped") != "kept" {
		t.Errorf("the raw header is not reachable: %v", got.Header)
	}
}

// MsgFrom finds nothing on a plain context or another adapter's.
func TestAMessageReadOnAForeignContextFindsNothing(t *testing.T) {
	if _, ok := MsgFrom(context.Background()); ok {
		t.Error("a plain context yielded a message")
	}
	type otherAdapterKey struct{}
	ctx := context.WithValue(context.Background(), otherAdapterKey{}, "a foreign record")
	if _, ok := MsgFrom(ctx); ok {
		t.Error("another adapter's delivery yielded a NATS message")
	}
}

// MustMsg panics, naming the cause, on a context with no message.
func TestMustMsgPanicsOnAForeignDelivery(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("MustMsg returned on a context with no message")
		}
		if msg, _ := r.(string); !strings.Contains(msg, "not nats") {
			t.Errorf("panic does not say what is wrong: %v", r)
		}
	}()
	MustMsg(context.Background())
}

// jetStreamDelivery stands in for a JetStream message; only its identity matters.
type jetStreamDelivery struct{ jetstream.Msg }

// MustJetStreamMsg returns the message a JetStream delivery carries.
func TestMustJetStreamMsgReturnsTheDeliveredMessage(t *testing.T) {
	m := &jetStreamDelivery{}
	if got := MustJetStreamMsg(withJetStreamMsg(context.Background(), m)); got != m {
		t.Errorf("MustJetStreamMsg = %v, want the delivered message", got)
	}
}

// MustJetStreamMsg panics, naming the cause, on a core NATS delivery.
func TestMustJetStreamMsgPanicsOnACoreDelivery(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("MustJetStreamMsg returned on a context with no JetStream message")
		}
		if msg, _ := r.(string); !strings.Contains(msg, "not JetStream") {
			t.Errorf("panic does not say what is wrong: %v", r)
		}
	}()
	MustJetStreamMsg(withMsg(context.Background(), &nats.Msg{Subject: "orders.Placed"}))
}
