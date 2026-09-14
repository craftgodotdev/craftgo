package kafka

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kversion"

	events "github.com/craftgodotdev/craftgo/pkg/events"
)

// closeCounter records how many times each client was closed, through
// franz-go's own hook rather than by watching for a crash - the two
// closes need not overlap to be wrong, and counting does not depend on
// whether they do.
type closeCounter struct {
	mu sync.Mutex
	n  map[*kgo.Client]int
}

func (c *closeCounter) OnClientClosed(cl *kgo.Client) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n == nil {
		c.n = map[*kgo.Client]int{}
	}
	c.n[cl]++
}

// twice reports how many clients were closed more than once.
func (c *closeCounter) twice() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, count := range c.n {
		if count > 1 {
			n++
		}
	}
	return n
}

// A consumer client has two closers - the read loop it belongs to, and
// [Transport.Close] - and kgo.Client.Close has no guard of its own, so
// closing twice runs one client's teardown against itself.
//
// This is the shutdown craftgo generates: wiring.Register's shutdown
// cancels the delivery context, the registered transport close runs after
// it, and nothing synchronises either against the read loops.
func TestEveryConsumerClientIsClosedExactlyOnce(t *testing.T) {
	for _, order := range []string{"cancel first", "close first"} {
		t.Run(order, func(t *testing.T) {
			const contract = "orders.Placed"
			addrs := cluster(t, contract, kversion.V4_2_0())
			counter := &closeCounter{}
			tr := New(addrs, WithClientOptions(kgo.WithHooks(counter)))

			ctx, cancel := context.WithCancel(context.Background())
			for _, c := range []string{contract, "orders.Cancelled", "catalog.PriceChanged"} {
				if err := tr.Subscribe(ctx, events.Subscription{
					Event: c, Consumer: "C", Group: "g-" + c,
					Handle: func(context.Context, *events.Message) error { return nil },
				}); err != nil {
					t.Fatalf("subscribe %s: %v", c, err)
				}
			}
			for i := 0; i < 20; i++ {
				publish(t, tr, contract, "k", []byte("{}"))
			}
			time.Sleep(500 * time.Millisecond)

			if order == "cancel first" {
				cancel()
				time.Sleep(200 * time.Millisecond)
				_ = tr.Close()
			} else {
				_ = tr.Close()
				time.Sleep(200 * time.Millisecond)
				cancel()
			}
			time.Sleep(500 * time.Millisecond)

			if n := counter.twice(); n != 0 {
				t.Errorf("%d client(s) closed more than once", n)
			}
		})
	}
}

// A read loop whose own context is cancelled closes its client rather
// than leaving it open until the transport goes. That is the exit
// [Transport.Close] does not reach, so letting Close own every client
// would leak this one.
func TestASubscriptionCancelledOnItsOwnClosesItsClient(t *testing.T) {
	const contract = "orders.Placed"
	addrs := cluster(t, contract, kversion.V4_2_0())
	counter := &closeCounter{}
	tr := New(addrs, WithClientOptions(kgo.WithHooks(counter)))
	defer func() { _ = tr.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	if err := tr.Subscribe(ctx, events.Subscription{
		Event: contract, Consumer: "C", Group: "g",
		Handle: func(context.Context, *events.Message) error { return nil },
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	cancel()
	deadline := time.Now().Add(10 * time.Second)
	for {
		counter.mu.Lock()
		closed := len(counter.n)
		counter.mu.Unlock()
		if closed > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the client is still open after its subscription's context was cancelled")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
