package kafka

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kversion"

	events "github.com/craftgodotdev/craftgo/pkg/events"
)

// batchesOf collects the batches a subscription was handed, as payload strings.
type batchesOf struct {
	mu   sync.Mutex
	got  [][]string
	seen chan struct{}
}

func newBatchesOf() *batchesOf { return &batchesOf{seen: make(chan struct{}, 64)} }

func (b *batchesOf) handle(_ context.Context, batch []*events.Message) error {
	payloads := make([]string, len(batch))
	for i, m := range batch {
		payloads[i] = string(m.Payload)
	}
	b.mu.Lock()
	b.got = append(b.got, payloads)
	b.mu.Unlock()
	b.seen <- struct{}{}
	return nil
}

// payloads returns every payload handed over, batch after batch.
func (b *batchesOf) payloads() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for _, batch := range b.got {
		out = append(out, batch...)
	}
	return out
}

// waitForPayloads blocks until n payloads have arrived, or fails the test.
func (b *batchesOf) waitForPayloads(t *testing.T, n int) {
	t.Helper()
	deadline := time.After(30 * time.Second)
	for len(b.payloads()) < n {
		select {
		case <-b.seen:
		case <-deadline:
			t.Fatalf("saw payloads %v, want %d", b.payloads(), n)
		}
	}
}

func batchSubscription(group string, size events.BatchSize, handle events.BatchHandler) events.Subscription {
	return events.Subscription{
		Event: "orders.Placed", Consumer: "C", Group: events.Group(group),
		Batch: &events.Batch{BatchSize: size, Handle: handle},
	}
}

// A classic group gathers a batch until it is full or its Wait has passed, in partition
// order, and commits it: the group does not read it again.
func TestAClassicGroupConsumesInBatches(t *testing.T) {
	const contract = "orders.Placed"
	addrs := cluster(t, contract, kversion.V4_2_0())
	first := New(addrs)
	for _, body := range []string{"1", "2", "3", "4", "5"} {
		publish(t, first, contract, "o-1", []byte(body))
	}

	got := newBatchesOf()
	ctx, cancel := context.WithCancel(context.Background())
	if err := first.Subscribe(ctx, []events.Subscription{
		batchSubscription("classic-batch", events.BatchSize{Max: 3, Wait: 500 * time.Millisecond}, got.handle),
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	got.waitForPayloads(t, 5)
	time.Sleep(time.Second)
	cancel()
	_ = first.Close()

	got.mu.Lock()
	batches := append([][]string(nil), got.got...)
	got.mu.Unlock()
	if len(batches) != 2 || strings.Join(batches[0], ",") != "1,2,3" || strings.Join(batches[1], ",") != "4,5" {
		t.Errorf("batches = %v, want [1 2 3] then [4 5]", batches)
	}

	second := New(addrs)
	defer func() { _ = second.Close() }()
	again := newBatchesOf()
	if err := second.Subscribe(t.Context(), []events.Subscription{
		batchSubscription("classic-batch", events.BatchSize{Max: 3, Wait: 500 * time.Millisecond}, again.handle),
	}); err != nil {
		t.Fatalf("resubscribe: %v", err)
	}
	time.Sleep(3 * time.Second)
	if p := again.payloads(); len(p) != 0 {
		t.Errorf("the group read %v again, want nothing - the batches were committed", p)
	}
}

// A share group answers each record of a batch: one redelivered comes back, one rejected
// never does, and the settled one is done.
func TestAShareGroupAnswersEachRecordOfABatch(t *testing.T) {
	const (
		contract = "orders.Placed"
		group    = "share-batch"
	)
	addrs := cluster(t, contract, kversion.V4_2_0())
	shareFromEarliest(t, addrs, group)
	tr := New(addrs, WithShareGroup(), WithMaxDeliveries(0))
	defer func() { _ = tr.Close() }()
	for _, body := range []string{"redeliver", "reject", "settle"} {
		publish(t, tr, contract, "o-1", []byte(body))
	}

	got := newBatchesOf()
	if err := tr.Subscribe(t.Context(), []events.Subscription{
		batchSubscription(group, events.BatchSize{Max: 3, Wait: 500 * time.Millisecond},
			func(ctx context.Context, batch []*events.Message) error {
				for _, m := range batch {
					switch string(m.Payload) {
					case "redeliver":
						if m.Deliveries() <= 1 {
							m.Redeliver()
						}
					case "reject":
						m.Reject()
					}
				}
				return got.handle(ctx, batch)
			}),
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	got.waitForPayloads(t, 4)
	time.Sleep(2 * time.Second)

	counts := map[string]int{}
	for _, p := range got.payloads() {
		counts[p]++
	}
	if counts["redeliver"] != 2 || counts["reject"] != 1 || counts["settle"] != 1 {
		t.Errorf("deliveries = %v, want redeliver twice, reject and settle once", counts)
	}
}

// A share batch slower than the record lock keeps its records while it renews them.
func TestASlowShareBatchKeepsItsRecords(t *testing.T) {
	const (
		contract = "orders.Placed"
		group    = "slow-batch"
	)
	addrs := lockCluster(t, contract)
	shareFromEarliest(t, addrs, group)
	tr := New(addrs, WithShareGroup(), WithLockRenewInterval(300*time.Millisecond))
	defer func() { _ = tr.Close() }()
	publish(t, tr, contract, "o-1", []byte("1"))
	publish(t, tr, contract, "o-1", []byte("2"))

	got := newBatchesOf()
	if err := tr.Subscribe(t.Context(), []events.Subscription{
		batchSubscription(group, events.BatchSize{Max: 2, Wait: 500 * time.Millisecond},
			func(ctx context.Context, batch []*events.Message) error {
				err := got.handle(ctx, batch)
				time.Sleep(3 * time.Second) // three times the lock
				return err
			}),
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	got.waitForPayloads(t, 2)
	time.Sleep(5 * time.Second)

	counts := map[string]int{}
	for _, p := range got.payloads() {
		counts[p]++
	}
	if counts["1"] != 1 || counts["2"] != 1 {
		t.Errorf("deliveries = %v, want each record once - a batch slower than the lock lost its records", counts)
	}
}

// A batch handler's error reaches the error handler once, with no message.
func TestABatchErrorIsReportedOnceOverKafka(t *testing.T) {
	const contract = "orders.Placed"
	addrs := cluster(t, contract, kversion.V4_2_0())
	reported := make(chan *events.Message, 8)
	tr := New(addrs, WithErrorHandler(func(_ events.Subscription, msg *events.Message, err error) {
		if err != nil && err.Error() == "store down" {
			reported <- msg
		}
	}))
	defer func() { _ = tr.Close() }()
	publish(t, tr, contract, "o-1", []byte("1"))
	publish(t, tr, contract, "o-1", []byte("2"))

	if err := tr.Subscribe(t.Context(), []events.Subscription{
		batchSubscription("failing", events.BatchSize{Max: 2, Wait: time.Second},
			func(context.Context, []*events.Message) error { return errors.New("store down") }),
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	select {
	case msg := <-reported:
		if msg != nil {
			t.Errorf("the batch error arrived with message %+v, want none", msg)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the batch error was never reported")
	}
	select {
	case <-reported:
		t.Error("one batch error was reported twice")
	case <-time.After(time.Second):
	}
}
