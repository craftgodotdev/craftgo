package kafka

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kversion"

	events "github.com/craftgodotdev/craftgo/pkg/events"
)

// A classic consumer group keeps ONE offset per partition, so the group
// commits past a record its handler failed on and no restart brings it
// back. This is the behaviour a share group exists to replace, and it is
// what makes classic mode at-most-once whatever a middleware returns.
func TestAClassicGroupCommitsPastAFailedRecord(t *testing.T) {
	const (
		contract = "orders.Placed"
		group    = "classic-drop"
	)
	addrs := cluster(t, contract, kversion.V4_2_0())

	first := New(addrs)
	got := newDeliveries()
	ctx, cancel := context.WithCancel(context.Background())
	if err := first.Subscribe(ctx, []events.Subscription{{
		Event: contract, Consumer: "C", Group: group,
		Handle: func(_ context.Context, msg *events.Message) error {
			got.add(msg)
			if string(msg.Payload) == `{"id":1}` {
				return errors.New("nothing can handle this one")
			}
			return nil
		},
	}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	for _, body := range []string{`{"id":1}`, `{"id":2}`, `{"id":3}`} {
		publish(t, first, contract, "o-1", []byte(body))
	}
	got.waitFor(t, 3)
	got.quiet(t, 2*time.Second, 3)
	cancel()
	_ = first.Close()

	// The same group again. Nothing comes back - not the failure, not
	// the two that succeeded after it.
	second := New(addrs)
	defer func() { _ = second.Close() }()
	back := newDeliveries()
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	if err := second.Subscribe(ctx2, []events.Subscription{{
		Event: contract, Consumer: "C", Group: group,
		Handle: func(_ context.Context, msg *events.Message) error { back.add(msg); return nil },
	}}); err != nil {
		t.Fatalf("resubscribe: %v", err)
	}
	back.quiet(t, 3*time.Second, 0)

	got.mu.Lock()
	defer got.mu.Unlock()
	if len(got.got) != 3 {
		t.Fatalf("%d deliveries, want 3", len(got.got))
	}
	for i, want := range []string{`{"id":1}`, `{"id":2}`, `{"id":3}`} {
		if string(got.got[i].Payload) != want {
			t.Errorf("delivery %d is %s, want %s", i+1, got.got[i].Payload, want)
		}
	}
}
