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

// RecordFrom finds nothing on a plain context or another adapter's.
func TestARecordReadOnAForeignContextFindsNothing(t *testing.T) {
	if _, ok := RecordFrom(context.Background()); ok {
		t.Error("a plain context yielded a record")
	}
	// Another adapter's key has the same layout but a different type.
	type otherAdapterKey struct{}
	ctx := context.WithValue(context.Background(), otherAdapterKey{}, "a foreign record")
	if _, ok := RecordFrom(ctx); ok {
		t.Error("another adapter's delivery yielded a Kafka record")
	}
}

// MustRecord panics, naming the cause, on a context with no record.
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

// MustRecord on another transport fails the first message with a PanicError.
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
	ctx := t.Context()
	if err := bus.Register(events.Subscription{
		Event: "orders.Placed", Consumer: "C", Group: "g",
		Handle: func(context.Context, *events.Message) error {
			mu.Lock()
			ran++
			mu.Unlock()
			return nil
		},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := bus.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
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
