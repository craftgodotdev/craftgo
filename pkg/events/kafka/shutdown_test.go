package kafka

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kversion"

	events "github.com/craftgodotdev/craftgo/pkg/events"
)

// outcomes counts, per payload, the handler runs that finished and those that failed.
type outcomes struct {
	mu   sync.Mutex
	ok   map[string]int
	fail map[string]int
}

func newOutcomes() *outcomes { return &outcomes{ok: map[string]int{}, fail: map[string]int{}} }

func (o *outcomes) mark(p string, ok bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if ok {
		o.ok[p]++
	} else {
		o.fail[p]++
	}
}

func (o *outcomes) okCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.ok)
}

// work is 30ms of work bound to ctx, as a database call is.
func (o *outcomes) work(ctx context.Context, msg *events.Message) error {
	select {
	case <-ctx.Done():
		o.mark(string(msg.Payload), false)
		return ctx.Err()
	case <-time.After(30 * time.Millisecond):
		o.mark(string(msg.Payload), true)
		return nil
	}
}

// waitForOK blocks until n payloads have been handled to the end, or fails the test.
func (o *outcomes) waitForOK(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for o.okCount() < n {
		if time.Now().After(deadline) {
			t.Fatalf("%d payloads handled, want %d", o.okCount(), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// closedCount is how many clients counter has seen closed.
func (c *clientCounter) closedCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.n)
}

// cancelAndWait cancels a subscription's context and waits for its read loop to close its
// client, once it has answered and committed what it handled.
func cancelAndWait(t *testing.T, cancel context.CancelFunc, counter *clientCounter) {
	t.Helper()
	before := counter.closedCount()
	cancel()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if counter.closedCount() > before {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the read loop did not stop after its context was cancelled")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Stopping a subscription mid-fetch, its context cancelled and then the transport closed,
// loses nothing: each record handled to the end is answered or committed, and every other
// comes back to the group's next consumer, which handles nothing twice.
func TestStoppingMidFetchLosesNothingAndRepeatsNothing(t *testing.T) {
	for _, mode := range []string{"classic", "share"} {
		t.Run(mode, func(t *testing.T) {
			const (
				contract = "orders.Placed"
				group    = "stopping"
				n        = 40
			)
			addrs := cluster(t, contract, kversion.V4_2_0())
			var opts []Option
			if mode == "share" {
				shareFromEarliest(t, addrs, group)
				opts = append(opts, WithShareGroup())
			}
			pub := New(addrs)
			defer func() { _ = pub.Close() }()
			for i := range n {
				publish(t, pub, contract, "k", []byte(fmt.Sprintf("%02d", i)))
			}
			subscribe := func(ctx context.Context, tr *Transport, o *outcomes) {
				t.Helper()
				if err := tr.Subscribe(ctx, []events.Subscription{{
					Event: contract, Consumer: "C", Group: group, Handle: o.work,
				}}); err != nil {
					t.Fatalf("subscribe: %v", err)
				}
			}

			first := newOutcomes()
			counter := &clientCounter{}
			tr := New(addrs, append(opts, WithClientOptions(kgo.WithHooks(counter)))...)
			ctx, cancel := context.WithCancel(context.Background())
			subscribe(ctx, tr, first)
			first.waitForOK(t, 5)
			cancelAndWait(t, cancel, counter)
			_ = tr.Close()

			second := newOutcomes()
			tr2 := New(addrs, opts...)
			defer func() { _ = tr2.Close() }()
			subscribe(t.Context(), tr2, second)
			second.waitForOK(t, n-first.okCount())
			time.Sleep(time.Second)

			first.mu.Lock()
			second.mu.Lock()
			defer first.mu.Unlock()
			defer second.mu.Unlock()
			for i := range n {
				p := fmt.Sprintf("%02d", i)
				switch {
				case first.ok[p] == 0 && second.ok[p] == 0:
					t.Errorf("record %s was never handled to the end", p)
				case first.ok[p] > 0 && second.ok[p] > 0:
					t.Errorf("record %s was handled again after the first run finished it", p)
				}
			}
		})
	}
}

// A batch whose handler the shutdown cut short comes back whole to the group's next
// consumer.
func TestABatchTheShutdownCutShortComesBack(t *testing.T) {
	for _, mode := range []string{"classic", "share"} {
		t.Run(mode, func(t *testing.T) {
			const (
				contract = "orders.Placed"
				group    = "stopping-batch"
			)
			addrs := cluster(t, contract, kversion.V4_2_0())
			var opts []Option
			if mode == "share" {
				shareFromEarliest(t, addrs, group)
				opts = append(opts, WithShareGroup())
			}
			pub := New(addrs)
			defer func() { _ = pub.Close() }()
			for _, p := range []string{"1", "2", "3"} {
				publish(t, pub, contract, "k", []byte(p))
			}
			size := events.BatchSize{Max: 10, Wait: 300 * time.Millisecond}

			started := make(chan struct{}, 1)
			counter := &clientCounter{}
			tr := New(addrs, append(opts, WithClientOptions(kgo.WithHooks(counter)))...)
			ctx, cancel := context.WithCancel(context.Background())
			if err := tr.Subscribe(ctx, []events.Subscription{batchSubscription(group, size,
				func(ctx context.Context, _ []*events.Message) error {
					started <- struct{}{}
					<-ctx.Done()
					return ctx.Err()
				})}); err != nil {
				t.Fatalf("subscribe: %v", err)
			}
			select {
			case <-started:
			case <-time.After(20 * time.Second):
				t.Fatal("no batch")
			}
			cancelAndWait(t, cancel, counter)
			_ = tr.Close()

			back := newBatchesOf()
			tr2 := New(addrs, opts...)
			defer func() { _ = tr2.Close() }()
			if err := tr2.Subscribe(t.Context(), []events.Subscription{batchSubscription(group, size, back.handle)}); err != nil {
				t.Fatalf("resubscribe: %v", err)
			}
			back.waitForPayloads(t, 3)
		})
	}
}

// Only a failure once the subscription's context has ended, with the chain asking for
// nothing or for another delivery, counts as cut short by the shutdown.
func TestOnlyAFailureAfterTheShutdownIsCutShort(t *testing.T) {
	live := context.Background()
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	failed := errors.New("write failed")
	asked := func(d func(*events.Message)) *events.Message {
		msg := &events.Message{}
		if d != nil {
			d(msg)
		}
		return msg
	}
	cases := []struct {
		name string
		ctx  context.Context
		msg  *events.Message
		err  error
		want bool
	}{
		{"failed after the end", ended, asked(nil), failed, true},
		{"asked for another delivery", ended, asked((*events.Message).Redeliver), failed, true},
		{"finished after the end", ended, asked(nil), nil, false},
		{"failed while live", live, asked(nil), failed, false},
		{"settled by the chain", ended, asked((*events.Message).Settle), failed, false},
		{"rejected by the chain", ended, asked((*events.Message).Reject), failed, false},
	}
	for _, c := range cases {
		if got := interrupted(c.ctx, c.msg, c.err); got != c.want {
			t.Errorf("%s: interrupted = %v, want %v", c.name, got, c.want)
		}
	}
}

// committable leaves out, per partition, the first record handed back and every one after
// it, and keeps the other partitions whole.
func TestCommittableStopsEachPartitionAtItsFirstRecordHandedBack(t *testing.T) {
	rec := func(partition int32, offset int64) *kgo.Record {
		return &kgo.Record{Topic: "orders.Placed", Partition: partition, Offset: offset}
	}
	recs := []*kgo.Record{rec(0, 1), rec(1, 7), rec(0, 2), rec(0, 3), rec(1, 8)}
	back := []bool{false, false, true, false, false}
	var got []string
	for _, r := range committable(recs, back) {
		got = append(got, fmt.Sprintf("%d/%d", r.Partition, r.Offset))
	}
	if fmt.Sprint(got) != "[0/1 1/7 1/8]" {
		t.Errorf("committable = %v, want [0/1 1/7 1/8]", got)
	}
}
