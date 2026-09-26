package kafka

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kversion"

	events "github.com/craftgodotdev/craftgo/pkg/events"
)

// lockCluster is cluster with a one-second share-record lock.
func lockCluster(t *testing.T, topic string) []string {
	t.Helper()
	c, err := kfake.NewCluster(
		kfake.NumBrokers(1),
		kfake.SeedTopics(1, topic),
		kfake.MaxVersions(kversion.V4_2_0()),
		kfake.BrokerConfigs(map[string]string{
			"group.share.record.lock.duration.ms": "1000",
			"share.record.lock.sweep.interval.ms": "200",
		}),
	)
	if err != nil {
		t.Fatalf("start broker: %v", err)
	}
	t.Cleanup(c.Close)
	return c.ListenAddrs()
}

// deliveriesOfOneSlowMessage returns the delivery counts of one slow record.
func deliveriesOfOneSlowMessage(t *testing.T, group string, work time.Duration, opts ...Option) []int {
	t.Helper()
	const contract = "orders.Placed"
	addrs := lockCluster(t, contract)
	shareFromEarliest(t, addrs, group)

	tr := New(addrs, append([]Option{WithShareGroup()}, opts...)...)
	defer func() { _ = tr.Close() }()
	publish(t, tr, contract, "o-1", []byte(`{}`))

	var mu sync.Mutex
	var counts []int
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: contract, Consumer: "C", Group: events.Group(group),
		Handle: func(_ context.Context, msg *events.Message) error {
			mu.Lock()
			counts = append(counts, msg.Deliveries())
			mu.Unlock()
			time.Sleep(work)
			return nil // succeeds, and asks for nothing
		},
	}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	time.Sleep(work + 5*time.Second)
	mu.Lock()
	defer mu.Unlock()
	return append([]int(nil), counts...)
}

// Lock renewal keeps a handler slower than the lock from losing its record.
func TestASlowHandlerKeepsItsRecord(t *testing.T) {
	got := deliveriesOfOneSlowMessage(t, "renewed", 4*time.Second, WithLockRenewInterval(300*time.Millisecond))
	if len(got) != 1 {
		t.Fatalf("one record was handled %d times, counts %v - the lock has to be held while the handler runs", len(got), got)
	}
}

// The delivery cap does not bound the redeliveries of a lapsed lock.
func TestTheDeliveryCapDoesNotBoundALockThatLapses(t *testing.T) {
	got := deliveriesOfOneSlowMessage(t, "capped", 4*time.Second,
		WithMaxDeliveries(2), WithLockRenewInterval(0))
	if len(got) < 2 {
		t.Fatalf("one record was handled %d times, counts %v - want several, which is the point", len(got), got)
	}
}

// The delivery cap defaults to 5 and lock renewal to every 10s.
func TestTheGuardDefaultsApplyWithoutBeingAskedFor(t *testing.T) {
	tr := New(nil, WithShareGroup())
	if tr.maxDeliveries != 5 {
		t.Errorf("maxDeliveries = %d, want 5 - the same cap the JetStream adapter defaults to", tr.maxDeliveries)
	}
	if tr.lockRenew != 10*time.Second {
		t.Errorf("lockRenew = %s, want 10s - shorter than the broker's 30s default lock", tr.lockRenew)
	}
}
