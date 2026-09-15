package nats

import (
	"context"
	"strings"
	"testing"

	"github.com/nats-io/nats.go"
)

// A middleware reaching for what events.Message does not carry - the
// reply subject, the subject a wildcard matched, an unmapped header -
// gets the message the delivery came from.
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

// The barrier is structural: the key type is unexported and distinct per
// adapter, so a NATS-typed read on a context that is not a NATS delivery
// cannot find anything.
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

// MustMsg turns a cross-transport install into a panic, which the bus
// recovers into a *PanicError naming the subscription.
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
