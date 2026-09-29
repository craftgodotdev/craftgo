package events_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
)

// batchSubscriber consumes in batches, redelivers, and hands each batch it is given
// straight to the subscription of its contract.
type batchSubscriber struct {
	recordingTransport
}

func (b *batchSubscriber) SubscribesBatches() bool { return true }

func (b *batchSubscriber) CanDisposition(d events.Disposition) bool {
	return d == events.DispositionSettle || d == events.DispositionRedeliver
}

// deliver hands msgs to the started batch subscription of their contract and returns
// what its handler returned.
func (b *batchSubscriber) deliver(t *testing.T, msgs ...*events.Message) error {
	t.Helper()
	for _, sub := range b.subs {
		if sub.Event == msgs[0].Event {
			return sub.Batch.Handle(context.Background(), msgs)
		}
	}
	t.Fatalf("no subscription of %s", msgs[0].Event)
	return nil
}

// batchBus builds a bus over a fresh batch transport.
func subscriberBus(opts ...events.Option) (*events.Bus, *batchSubscriber) {
	tr := &batchSubscriber{}
	opts = append([]events.Option{events.WithTransport(tr), events.WithCodec(codecjson.Codec{})}, opts...)
	return events.New(opts...), tr
}

// batchSub is a batch subscription of event in group whose handler does nothing.
func batchSub(event string, group events.Group) events.Subscription {
	return events.Subscription{
		Event: event, Consumer: event, Group: group,
		Batch: &events.Batch{
			BatchSize: events.BatchSize{Max: 10, Wait: time.Second},
			Handle:    func(context.Context, []*events.Message) error { return nil },
		},
	}
}

// oneSub is a subscription taking one message at a time.
func oneSub(event string, group events.Group) events.Subscription {
	return events.Subscription{
		Event: event, Consumer: event, Group: group,
		Handle: func(context.Context, *events.Message) error { return nil },
	}
}

// A transport that does not consume in batches refuses a batch subscription.
func TestRegisterRefusesABatchOnATransportWithoutBatches(t *testing.T) {
	bus, _ := busOver()
	err := bus.Register(batchSub("orders.Placed", "bulk"))
	if !errors.Is(err, events.ErrBatchUnsupported) {
		t.Fatalf("register = %v, want ErrBatchUnsupported", err)
	}
}

// A batch with no Max, no Wait, no handler, or a Handle beside it is refused.
func TestRegisterRefusesAnInvalidBatch(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(*events.Subscription)
		want error
	}{
		{"max", func(s *events.Subscription) { s.Batch.Max = 0 }, events.ErrInvalidBatch},
		{"wait", func(s *events.Subscription) { s.Batch.Wait = 0 }, events.ErrInvalidBatch},
		{"handle beside", func(s *events.Subscription) { s.Handle = oneSub("x", "g").Handle }, events.ErrInvalidBatch},
		{"chain beside", func(s *events.Subscription) { s.Chain = events.NewChain(events.Recover()) }, events.ErrInvalidBatch},
		{"no handler", func(s *events.Subscription) { s.Batch.Handle = nil }, events.ErrNoHandler},
	} {
		t.Run(c.name, func(t *testing.T) {
			bus, _ := subscriberBus()
			sub := batchSub("orders.Placed", "bulk")
			batch := *sub.Batch
			sub.Batch = &batch
			c.edit(&sub)
			if err := bus.Register(sub); !errors.Is(err, c.want) {
				t.Fatalf("register = %v, want %v", err, c.want)
			}
		})
	}
}

// A batch subscription holds its group alone, whichever registers first.
func TestABatchSubscriptionHoldsItsGroupAlone(t *testing.T) {
	for _, c := range []struct {
		name          string
		first, second events.Subscription
		want          error
	}{
		{"batch then one", batchSub("orders.Placed", "g"), oneSub("orders.Shipped", "g"), events.ErrBatchGroupShared},
		{"one then batch", oneSub("orders.Placed", "g"), batchSub("orders.Shipped", "g"), events.ErrBatchGroupShared},
		{"two batches", batchSub("orders.Placed", "g"), batchSub("orders.Shipped", "g"), events.ErrBatchGroupShared},
		{"the same twice", batchSub("orders.Placed", "g"), batchSub("orders.Placed", "g"), events.ErrDuplicateSubscription},
		{"other group", batchSub("orders.Placed", "g"), oneSub("orders.Placed", "h"), nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			bus, _ := subscriberBus()
			if err := bus.Register(c.first); err != nil {
				t.Fatalf("first: %v", err)
			}
			if err := bus.Register(c.second); !errors.Is(err, c.want) {
				t.Fatalf("second = %v, want %v", err, c.want)
			}
		})
	}
}

// A batch handler runs inside the bus batch chain, then its own, and never inside a
// per-message chain.
func TestABatchIsWrappedInTheBatchChains(t *testing.T) {
	var trace string
	mark := func(tag string) events.BatchMiddleware {
		return func(_ events.Subscription, next events.BatchHandler) events.BatchHandler {
			return func(ctx context.Context, batch []*events.Message) error {
				trace += ">" + tag
				err := next(ctx, batch)
				trace += "<" + tag
				return err
			}
		}
	}
	bus, tr := subscriberBus(events.WithBatchMiddleware(mark("bus")), events.WithMiddleware(tagMW(&trace, "one")))
	bus.UseBatch(mark("use"))
	sub := batchSub("orders.Placed", "bulk")
	sub.Batch.Chain = events.NewBatchChain(mark("sub"))
	sub.Batch.Handle = func(context.Context, []*events.Message) error {
		trace += "|H|"
		return nil
	}
	start(t, context.Background(), bus, sub)
	if err := tr.deliver(t, &events.Message{Event: "orders.Placed"}); err != nil {
		t.Fatal(err)
	}
	if want := ">bus>use>sub|H|<sub<use<bus"; trace != want {
		t.Errorf("trace = %q, want %q", trace, want)
	}
}

// A panic in a batch handler leaves every message's answer to the chain; one escaping the
// chain asks for each to be redelivered.
func TestABatchPanicIsRecovered(t *testing.T) {
	panicky := func(context.Context, []*events.Message) error { panic("boom") }
	settleAll := func(_ events.Subscription, next events.BatchHandler) events.BatchHandler {
		return func(ctx context.Context, batch []*events.Message) error {
			for _, m := range batch {
				m.Settle()
			}
			return next(ctx, batch)
		}
	}
	for _, c := range []struct {
		name    string
		chain   events.BatchChain
		handle  events.BatchHandler
		wantErr bool
		want    events.Disposition
	}{
		{"handler", events.NewBatchChain(settleAll), panicky, true, events.DispositionUnset},
		{"chain", events.NewBatchChain(func(events.Subscription, events.BatchHandler) events.BatchHandler { return panicky }), nil, true, events.DispositionRedeliver},
	} {
		t.Run(c.name, func(t *testing.T) {
			bus, tr := subscriberBus()
			sub := batchSub("orders.Placed", "bulk")
			sub.Batch.Chain = c.chain
			if c.handle != nil {
				sub.Batch.Handle = c.handle
			}
			start(t, context.Background(), bus, sub)
			msgs := []*events.Message{{Event: "orders.Placed"}, {Event: "orders.Placed"}}
			err := tr.deliver(t, msgs...)
			var pe *events.PanicError
			if !errors.As(err, &pe) || pe.Value != "boom" || pe.Group != "bulk" {
				t.Fatalf("err = %v, want the PanicError of group bulk", err)
			}
			for i, m := range msgs {
				if m.Disposition() != c.want {
					t.Errorf("message %d disposition = %v, want %v", i, m.Disposition(), c.want)
				}
			}
		})
	}
}

// The typed batch handler hands fn the payloads that decode and validate, and names every
// failure by its index into the whole batch.
func TestATypedBatchNamesEachFailure(t *testing.T) {
	bus, tr := subscriberBus()
	failRest := errors.New("store down")
	var seen []string
	err := orderPlaced.SubscribeBatch(bus, "bulk", events.BatchSize{Max: 10, Wait: time.Second},
		func(_ context.Context, batch []events.Item[order]) error {
			for _, it := range batch {
				seen = append(seen, it.Payload.ID)
				if it.Payload.ID == "b" {
					it.Fail(errors.New("b is on hold"))
				}
			}
			return failRest
		})
	if err != nil {
		t.Fatal(err)
	}
	start(t, context.Background(), bus)
	msg := func(body string) *events.Message {
		return &events.Message{Event: "orders.Placed", Payload: []byte(body)}
	}
	got := tr.deliver(t, msg(`{"id":"a"}`), msg(`not json`), msg(`{"id":"b"}`), msg(`{"id":""}`), msg(`{"id":"c"}`))
	if strings.Join(seen, ",") != "a,b,c" {
		t.Errorf("fn saw %v, want a,b,c", seen)
	}
	var itemErrs events.ItemErrors
	if !errors.As(got, &itemErrs) || len(itemErrs) != 5 {
		t.Fatalf("err = %v, want ItemErrors naming all five", got)
	}
	var pe *events.PayloadError
	if !errors.As(itemErrs[1], &pe) || !errors.As(itemErrs[3], &pe) {
		t.Errorf("undecodable and invalid = %v, %v, want PayloadErrors", itemErrs[1], itemErrs[3])
	}
	if itemErrs[2] == nil || itemErrs[2].Error() != "b is on hold" {
		t.Errorf("failed item = %v, want its own error", itemErrs[2])
	}
	if !errors.Is(itemErrs[0], failRest) || !errors.Is(itemErrs[4], failRest) {
		t.Errorf("rest = %v, %v, want the batch's error", itemErrs[0], itemErrs[4])
	}
	if !errors.Is(got, failRest) {
		t.Error("errors.Is does not reach an item's error")
	}
}

// fn does not run when no message of the batch decodes, and a clean batch returns nil.
func TestATypedBatchRunsOnlyWithValidItems(t *testing.T) {
	bus, tr := subscriberBus()
	calls := 0
	if err := orderPlaced.SubscribeBatch(bus, "bulk", events.BatchSize{Max: 10, Wait: time.Second},
		func(_ context.Context, batch []events.Item[order]) error {
			calls++
			for _, it := range batch {
				it.Fail(errors.New("first"))
				it.Fail(nil)
			}
			return nil
		}); err != nil {
		t.Fatal(err)
	}
	start(t, context.Background(), bus)
	if err := tr.deliver(t, &events.Message{Event: "orders.Placed", Payload: []byte(`{"id":""}`)}); err == nil || calls != 0 {
		t.Errorf("invalid batch: err %v, calls %d, want an error and no call", err, calls)
	}
	if err := tr.deliver(t, &events.Message{Event: "orders.Placed", Payload: []byte(`{"id":"a"}`)}); err != nil || calls != 1 {
		t.Errorf("clean batch: err %v, calls %d, want nil and one call", err, calls)
	}
}

// ItemErrors lists the first failures in index order.
func TestItemErrorsReadInIndexOrder(t *testing.T) {
	err := events.ItemErrors{7: errors.New("g"), 0: errors.New("a"), 3: errors.New("d"), 5: errors.New("f")}
	want := "events: 4 message(s) of the batch failed: [0] a; [3] d; [5] f; and 1 more"
	if err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
	if got := err.Unwrap(); len(got) != 4 || got[3].Error() != "g" {
		t.Errorf("Unwrap() = %v, want four in index order", got)
	}
}

// The plan shows a batch consumer's bounds.
func TestPlanShowsTheBatchBounds(t *testing.T) {
	bus, _ := subscriberBus()
	sub := batchSub("orders.Placed", "bulk")
	sub.Batch.BatchSize = events.BatchSize{Max: 100, Wait: 1500 * time.Millisecond}
	if err := bus.Register(sub); err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(bus.Plan())
	if err != nil {
		t.Fatal(err)
	}
	want := `{"groups":[{"name":"bulk","consumers":[{"event":"orders.Placed","consumer":"orders.Placed","batch":{"max":100,"wait":"1.5s"}}]}]}`
	if string(got) != want {
		t.Errorf("plan = %s\nwant    %s", got, want)
	}
}

// UseBatch after Start panics, as Use does.
func TestUseBatchAfterStartPanics(t *testing.T) {
	bus, _ := subscriberBus()
	start(t, context.Background(), bus, batchSub("orders.Placed", "bulk"))
	defer func() {
		if recover() == nil {
			t.Error("UseBatch after Start did not panic")
		}
	}()
	bus.UseBatch(nil)
}

// redeliverFailures is the batch middleware of the events guide: each message the batch
// failed comes back, but one whose payload cannot decode, which fails the same way again.
func redeliverFailures(_ events.Subscription, next events.BatchHandler) events.BatchHandler {
	return func(ctx context.Context, batch []*events.Message) error {
		err := next(ctx, batch)
		var failed events.ItemErrors
		if !errors.As(err, &failed) {
			return err
		}
		for i, itemErr := range failed {
			var bad *events.PayloadError
			switch {
			case batch[i].Disposition() != events.DispositionUnset:
			case errors.As(itemErr, &bad):
				batch[i].Reject()
			default:
				batch[i].Redeliver()
			}
		}
		return err
	}
}

// The guide's batch middleware redelivers each failure, rejects an undecodable payload and
// leaves a message the handler answered alone.
func TestTheGuidesBatchMiddleware(t *testing.T) {
	bus, tr := subscriberBus(events.WithBatchMiddleware(redeliverFailures))
	if err := orderPlaced.SubscribeBatch(bus, "bulk", events.BatchSize{Max: 10, Wait: time.Second},
		func(_ context.Context, batch []events.Item[order]) error {
			for _, it := range batch {
				if it.Payload.ID == "held" {
					it.Msg.Settle()
				}
			}
			return errors.New("store down")
		}); err != nil {
		t.Fatal(err)
	}
	start(t, context.Background(), bus)
	msgs := []*events.Message{
		{Event: "orders.Placed", Payload: []byte(`{"id":"a"}`)},
		{Event: "orders.Placed", Payload: []byte(`nope`)},
		{Event: "orders.Placed", Payload: []byte(`{"id":"held"}`)},
	}
	_ = tr.deliver(t, msgs...)
	want := []events.Disposition{events.DispositionRedeliver, events.DispositionReject, events.DispositionSettle}
	for i, m := range msgs {
		if m.Disposition() != want[i] {
			t.Errorf("message %d disposition = %v, want %v", i, m.Disposition(), want[i])
		}
	}
}
