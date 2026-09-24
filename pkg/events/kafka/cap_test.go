package kafka

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kversion"

	events "github.com/craftgodotdev/craftgo/pkg/events"
)

// The error handler hears when the cap turns a Redeliver into a reject.
func TestTheDeliveryCapReportsThatItFired(t *testing.T) {
	const (
		contract = "orders.Placed"
		group    = "capped-report"
		cap      = 2
	)
	addrs := cluster(t, contract, kversion.V4_2_0())
	shareFromEarliest(t, addrs, group)

	var (
		mu    sync.Mutex
		errs  []error
		msgs  []*events.Message
		onErr = func(_ events.Subscription, msg *events.Message, err error) {
			mu.Lock()
			defer mu.Unlock()
			errs = append(errs, err)
			msgs = append(msgs, msg)
		}
	)
	tr := New(addrs, WithShareGroup(), WithMaxDeliveries(cap), WithErrorHandler(onErr))
	defer func() { _ = tr.Close() }()
	publish(t, tr, contract, "o-1", []byte(`{}`))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: contract, Consumer: "C", Group: group,
		Handle: func(_ context.Context, msg *events.Message) error {
			msg.Redeliver() // never gives up
			return nil      // and never fails, so nothing else reports
		},
	}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	deadline := time.Now().Add(20 * time.Second)
	for {
		mu.Lock()
		got := len(errs)
		mu.Unlock()
		if got > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the cap fired and nothing was reported")
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if n := len(errs); n != 1 {
		t.Fatalf("%d reports, want 1", n)
	}
	text := errs[0].Error()
	for _, want := range []string{contract, "2 deliveries", "WithMaxDeliveries is 2"} {
		if !strings.Contains(text, want) {
			t.Errorf("report %q does not name %q", text, want)
		}
	}
	if msgs[0] == nil || msgs[0].Key != "o-1" {
		t.Errorf("the report does not carry the record it is about")
	}
}
