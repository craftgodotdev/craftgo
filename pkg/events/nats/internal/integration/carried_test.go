package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	events "github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
	craftnats "github.com/craftgodotdev/craftgo/pkg/events/nats"
)

// natsBus builds a bus over core NATS or JetStream, on a server of its own with the
// ORDERS stream provisioned.
func natsBus(t *testing.T, kind string) *events.Bus {
	t.Helper()
	var tr interface {
		events.Publisher
		events.Subscriber
	}
	if kind == "JetStream" {
		conn := runJetStreamServer(t)
		provision(t, conn, "ORDERS", "orders.>")
		tr = jsTransport(t, conn)
	} else {
		core := craftnats.New(runServer(t))
		t.Cleanup(func() { _ = core.Close() })
		tr = core
	}
	return events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))
}

// What a NATS transport accepts arrives byte for byte: key, dedup ID and metadata.
func TestWhatNATSCarriesArrivesAsWritten(t *testing.T) {
	for _, kind := range []string{"core", "JetStream"} {
		t.Run(kind, func(t *testing.T) {
			bus := natsBus(t, kind)
			got := make(chan *events.Message, 1)
			start(t, t.Context(), bus, events.Subscription{
				Event: "orders.Placed", Consumer: "C", Group: "carried",
				Handle: func(_ context.Context, m *events.Message) error { got <- m; return nil },
			})
			time.Sleep(300 * time.Millisecond)

			meta := map[string]string{"x-inner": "a  b\tc", "x-empty": "", "x-uni": "ü→✓", "X-Mixed_Case.1~": `"a"=1;b,c:d`}
			opts := []events.PublishOption{events.WithKey("order 1"), events.WithDedupID("attempt ü 1")}
			for k, v := range meta {
				opts = append(opts, events.WithHeader(k, v))
			}
			if err := bus.Publish(context.Background(), "orders.Placed", payload{OrderID: "o-1"}, opts...); err != nil {
				t.Fatalf("publish: %v", err)
			}
			select {
			case m := <-got:
				if m.Key != "order 1" || m.DedupID != "attempt ü 1" {
					t.Errorf("key %q, dedup ID %q, want them as written", m.Key, m.DedupID)
				}
				for k, v := range meta {
					if got, ok := m.Metadata[k]; !ok || got != v {
						t.Errorf("metadata %q = %q (present %v), want %q", k, got, ok, v)
					}
				}
			case <-time.After(10 * time.Second):
				t.Fatal("no delivery")
			}
		})
	}
}

// What a NATS transport would change on the way fails the publish, and nothing arrives.
func TestWhatNATSWouldChangeIsNeverSent(t *testing.T) {
	for _, kind := range []string{"core", "JetStream"} {
		t.Run(kind, func(t *testing.T) {
			bus := natsBus(t, kind)
			got := make(chan *events.Message, 8)
			start(t, t.Context(), bus, events.Subscription{
				Event: "orders.Placed", Consumer: "C", Group: "refused",
				Handle: func(_ context.Context, m *events.Message) error { got <- m; return nil },
			})
			time.Sleep(300 * time.Millisecond)

			for what, opt := range map[string]events.PublishOption{
				"padded key":            events.WithKey(" k "),
				"dedup ID with a break": events.WithDedupID("d\n1"),
				"metadata key":          events.WithHeader("x trace", "v"),
				"metadata value":        events.WithHeader("x-pad", "  v  "),
			} {
				err := bus.Publish(context.Background(), "orders.Placed", payload{OrderID: what}, opt)
				if !errors.Is(err, craftnats.ErrNotCarried) {
					t.Errorf("%s: err = %v, want ErrNotCarried", what, err)
				}
			}
			err := bus.PublishAll(context.Background(), []events.Envelope{
				{Event: "orders.Placed", Payload: payload{OrderID: "fine"}},
				{Event: "orders.Placed", Payload: payload{OrderID: "padded"}, Key: "k "},
			})
			var refused *events.EnvelopeError
			if !errors.As(err, &refused) || refused.Index != 1 || !errors.Is(err, craftnats.ErrNotCarried) {
				t.Errorf("batch: err = %v, want an *events.EnvelopeError naming envelope 1", err)
			}
			select {
			case m := <-got:
				t.Errorf("%s arrived, though its publish failed", m.Payload)
			case <-time.After(time.Second):
			}
		})
	}
}
