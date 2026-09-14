package kafka

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	events "github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
	"github.com/craftgodotdev/craftgo/pkg/events/memory"
)

// The barrier is structural: the key type is unexported and distinct per
// adapter, so a Kafka-typed read on a context that is not a Kafka
// delivery cannot find anything. It is not that the lookup misses - no
// value of this type was ever stored.
func TestARecordReadOnAForeignContextFindsNothing(t *testing.T) {
	if _, ok := RecordFrom(context.Background()); ok {
		t.Error("a plain context yielded a record")
	}
	// A value stored under ANOTHER adapter's key shape: same struct{}
	// layout, different named type, so it cannot be read as this one.
	type otherAdapterKey struct{}
	ctx := context.WithValue(context.Background(), otherAdapterKey{}, "a foreign record")
	if _, ok := RecordFrom(ctx); ok {
		t.Error("another adapter's delivery yielded a Kafka record")
	}
}

// MustRecord turns a cross-transport install into a panic, which the bus
// recovers into a *PanicError naming the consumer, the group and the
// contract - loud on the first message rather than a quiet no-op.
func TestMustRecordPanicsOnAForeignDelivery(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("MustRecord returned on a context with no record")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "not kafka") {
			t.Errorf("panic does not say what is wrong: %v", r)
		}
	}()
	MustRecord(context.Background())
}

// THE SCENARIO THE BARRIER IS FOR, end to end: a Kafka-only middleware
// installed on a transport that is not Kafka. MustRecord panics, the
// bus's recover turns that into a *PanicError naming the consumer, the
// group and the contract, and the transport's error handler is told - on
// the FIRST message, not after a quiet week of reading nothing.
func TestAKafkaMiddlewareOnAnotherTransportFailsLoudlyAtOnce(t *testing.T) {
	var (
		mu     sync.Mutex
		failed error
		ran    int
	)
	tr := memory.New(memory.WithErrorHandler(func(_ events.Subscription, _ *events.Message, err error) {
		mu.Lock()
		failed = err
		mu.Unlock()
	}))
	bus := events.New(
		events.WithTransport(tr),
		events.WithCodec(codecjson.Codec{}),
		events.WithMiddleware(func(_ events.Subscription, next events.Handler) events.Handler {
			return func(ctx context.Context, msg *events.Message) error {
				rec := MustRecord(ctx) // wrong transport: this panics
				_ = rec
				return next(ctx, msg)
			}
		}),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := bus.Subscribe(ctx, events.Subscription{
		Event: "orders.Placed", Consumer: "C", Group: "g",
		Handle: func(context.Context, *events.Message) error {
			mu.Lock()
			ran++
			mu.Unlock()
			return nil
		},
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := tr.Publish(context.Background(), &events.Message{
		Event: "orders.Placed", Payload: []byte(`{}`),
		Metadata: map[string]string{events.MetaCodec: "json"},
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	tr.Drain()

	mu.Lock()
	defer mu.Unlock()
	var panicked *events.PanicError
	if !errors.As(failed, &panicked) {
		t.Fatalf("error handler saw %v, want a *PanicError", failed)
	}
	if panicked.Consumer != "C" || panicked.Group != "g" || panicked.Event != "orders.Placed" {
		t.Errorf("the panic does not name the subscription: %+v", panicked)
	}
	if ran != 0 {
		t.Errorf("the handler ran %d times - the chain broke before it", ran)
	}
}
