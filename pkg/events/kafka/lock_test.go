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

// lockCluster is [cluster] with a short acquisition lock, so a handler
// slower than the lock is reached in seconds rather than in the half
// minute a real broker's default would take.
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

// deliveriesOfOneSlowMessage publishes one record, hands it to a handler
// that takes work to run and always succeeds, and reports the delivery
// count of every delivery the broker made.
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
	if err := tr.Subscribe(ctx, events.Subscription{
		Event: contract, Consumer: "C", Group: group,
		Handle: func(_ context.Context, msg *events.Message) error {
			mu.Lock()
			counts = append(counts, msg.Deliveries())
			mu.Unlock()
			time.Sleep(work)
			return nil // succeeds, and asks for nothing
		},
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	time.Sleep(work + 5*time.Second)
	mu.Lock()
	defer mu.Unlock()
	return append([]int(nil), counts...)
}

// A share group holds each record under an acquisition lock, and a
// handler slower than that lock loses the record mid-flight: the broker
// hands the same one out again while this consumer is still working, and
// again every lock period after that. The work is done several times and
// every copy but one is wasted.
//
// Renewing the lock while the handler runs is what stops it. Measured
// against a one-second lock with a four-second handler: three deliveries
// of one record without renewal, one with.
func TestASlowHandlerKeepsItsRecord(t *testing.T) {
	got := deliveriesOfOneSlowMessage(t, "renewed", 4*time.Second, WithLockRenewInterval(300*time.Millisecond))
	if len(got) != 1 {
		t.Fatalf("one record was handled %d times, counts %v - the lock has to be held while the handler runs", len(got), got)
	}
}

// WithMaxDeliveries does NOT bound this, and it is not meant to. It caps
// redelivery a middleware ASKED for, and a lock that lapses under a slow
// handler is not that: the chain decided nothing, the disposition is
// unset, and every delivery is accepted. Capping a success instead would
// throw away work that had just succeeded without preventing a single one
// of the duplicate runs, which all happen before any ack.
//
// This test is why the cap was left where it is. It goes red if someone
// moves it, by showing the cap making no difference to the loop it
// supposedly bounds.
func TestTheDeliveryCapDoesNotBoundALockThatLapses(t *testing.T) {
	got := deliveriesOfOneSlowMessage(t, "capped", 4*time.Second,
		WithMaxDeliveries(2), WithLockRenewInterval(0))
	if len(got) < 2 {
		t.Fatalf("one record was handled %d times, counts %v - want several, which is the point", len(got), got)
	}
}

// Both guards apply without being asked for, which is the whole of what
// makes them guards: a middleware author who never reaches for either
// still gets them. Neither is reachable from a test that passes its own
// value, so the defaults are pinned here.
func TestTheGuardDefaultsApplyWithoutBeingAskedFor(t *testing.T) {
	tr := New(nil, WithShareGroup())
	if tr.maxDeliveries != 5 {
		t.Errorf("maxDeliveries = %d, want 5 - the same cap the JetStream adapter defaults to", tr.maxDeliveries)
	}
	if tr.lockRenew != 10*time.Second {
		t.Errorf("lockRenew = %s, want 10s - shorter than the broker's 30s default lock", tr.lockRenew)
	}
}
