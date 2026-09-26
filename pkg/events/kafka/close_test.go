package kafka

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kversion"

	events "github.com/craftgodotdev/craftgo/pkg/events"
)

// clientCounter counts the clients opened and each client's closes through franz-go's hooks.
type clientCounter struct {
	mu     sync.Mutex
	opened int
	n      map[*kgo.Client]int
}

func (c *clientCounter) OnNewClient(*kgo.Client) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.opened++
}

func (c *clientCounter) OnClientClosed(cl *kgo.Client) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n == nil {
		c.n = map[*kgo.Client]int{}
	}
	c.n[cl]++
}

// openedCount reports how many clients were opened.
func (c *clientCounter) openedCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.opened
}

// twice reports how many clients were closed more than once.
func (c *clientCounter) twice() int {
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

// A consumer client is closed once whether ctx is cancelled or Close runs first.
func TestEveryConsumerClientIsClosedExactlyOnce(t *testing.T) {
	for _, order := range []string{"cancel first", "close first"} {
		t.Run(order, func(t *testing.T) {
			const contract = "orders.Placed"
			addrs := cluster(t, contract, kversion.V4_2_0())
			counter := &clientCounter{}
			tr := New(addrs, WithClientOptions(kgo.WithHooks(counter)))

			ctx, cancel := context.WithCancel(context.Background())
			for _, c := range []string{contract, "orders.Cancelled", "catalog.PriceChanged"} {
				if err := tr.Subscribe(ctx, []events.Subscription{{
					Event: c, Consumer: "C", Group: events.Group("g-" + c),
					Handle: func(context.Context, *events.Message) error { return nil },
				}}); err != nil {
					t.Fatalf("subscribe %s: %v", c, err)
				}
			}
			for range 20 {
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
			counter.mu.Lock()
			opened, closed := counter.opened, len(counter.n)
			counter.mu.Unlock()
			if closed != opened {
				t.Errorf("%d of %d clients closed, want every one", closed, opened)
			}
		})
	}
}

func TestASubscriptionCancelledOnItsOwnClosesItsClient(t *testing.T) {
	const contract = "orders.Placed"
	addrs := cluster(t, contract, kversion.V4_2_0())
	counter := &clientCounter{}
	tr := New(addrs, WithClientOptions(kgo.WithHooks(counter)))
	defer func() { _ = tr.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: contract, Consumer: "C", Group: "g",
		Handle: func(context.Context, *events.Message) error { return nil },
	}}); err != nil {
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

// After Close, a publish, a batch and a subscribe return ErrClosed and open no client.
func TestPublishAndSubscribeAfterCloseAreRefused(t *testing.T) {
	const contract = "orders.Placed"
	counter := &clientCounter{}
	tr := New(cluster(t, contract, kversion.V4_2_0()), WithClientOptions(kgo.WithHooks(counter)))
	publish(t, tr, contract, "k", []byte(`{}`))
	if err := tr.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	opened := counter.openedCount()

	msg := &events.Message{Event: contract, Payload: []byte(`{}`)}
	if err := tr.Publish(context.Background(), msg); !errors.Is(err, ErrClosed) {
		t.Errorf("Publish after Close: err = %v, want ErrClosed", err)
	}
	if err := tr.PublishBatch(context.Background(), []*events.Message{msg}); !errors.Is(err, ErrClosed) {
		t.Errorf("PublishBatch after Close: err = %v, want ErrClosed", err)
	}
	ctx := t.Context()
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: contract, Consumer: "C", Group: "late",
		Handle: func(context.Context, *events.Message) error { return nil },
	}}); !errors.Is(err, ErrClosed) {
		t.Errorf("Subscribe after Close: err = %v, want ErrClosed", err)
	}
	if n := counter.openedCount() - opened; n != 0 {
		t.Errorf("%d client(s) opened after Close", n)
	}
}

// bufferedHook signals each record the producer takes.
type bufferedHook chan struct{}

func (h bufferedHook) OnProduceRecordBuffered(*kgo.Record) {
	select {
	case h <- struct{}{}:
	default:
	}
}

// A publish or a batch still waiting on the broker when Close runs returns ErrClosed, and
// franz-go's own error stays reachable.
func TestAPublishRacingCloseReportsErrClosed(t *testing.T) {
	msg := &events.Message{Event: "orders.Placed", Payload: []byte(`{}`)}
	for name, publish := range map[string]func(*Transport) error{
		"Publish":      func(tr *Transport) error { return tr.Publish(context.Background(), msg) },
		"PublishBatch": func(tr *Transport) error { return tr.PublishBatch(context.Background(), []*events.Message{msg}) },
	} {
		t.Run(name, func(t *testing.T) {
			buffered := make(bufferedHook, 1)
			tr := New([]string{"127.0.0.1:1"}, WithClientOptions(kgo.WithHooks(buffered)))
			done := make(chan error, 1)
			go func() { done <- publish(tr) }()
			select {
			case <-buffered:
			case <-time.After(10 * time.Second):
				t.Fatal("the record never reached the producer")
			}
			if err := tr.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}
			select {
			case err := <-done:
				if !errors.Is(err, ErrClosed) || !errors.Is(err, kgo.ErrClientClosed) {
					t.Errorf("err = %v, want ErrClosed wrapping kgo.ErrClientClosed", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("the publish outlived Close")
			}
		})
	}
}

// A consumer client that finishes opening after Close is refused, not kept for a Close that has run.
func TestAClientOpenedDuringCloseIsNotKept(t *testing.T) {
	tr := New(nil)
	cl, err := kgo.NewClient(kgo.SeedBrokers("127.0.0.1:1"))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer cl.Close()
	if err := tr.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if tr.track(cl) {
		t.Error("the transport kept a client after Close, so nothing would close it")
	}
}
