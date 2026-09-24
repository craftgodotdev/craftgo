package events_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	events "github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
)

// noop is a handler that does nothing.
func noop() events.Handler {
	return func(context.Context, *events.Message) error { return nil }
}

// busOver builds a bus over a fresh recording transport.
func busOver(opts ...events.Option) (*events.Bus, *recordingTransport) {
	tr := &recordingTransport{}
	opts = append([]events.Option{events.WithTransport(tr), events.WithCodec(codecjson.Codec{})}, opts...)
	return events.New(opts...), tr
}

// One Start is one call to the transport, carrying every subscription in
// group, contract then consumer order.
func TestStartHandsTheTransportOneSortedBatch(t *testing.T) {
	bus, tr := busOver()
	start(t, context.Background(), bus,
		events.Subscription{Event: "b.Two", Consumer: "Z", Group: "g2", Handle: noop()},
		events.Subscription{Event: "a.Two", Consumer: "B", Group: "g1", Handle: noop()},
		events.Subscription{Event: "a.One", Consumer: "A", Group: "g1", Handle: noop()},
	)
	if tr.batches != 1 {
		t.Fatalf("the transport was called %d times, want one batch", tr.batches)
	}
	var got []string
	for _, sub := range tr.subs {
		got = append(got, string(sub.Group)+"/"+sub.Event)
	}
	if want := "g1/a.One,g1/a.Two,g2/b.Two"; strings.Join(got, ",") != want {
		t.Errorf("order = %v, want %s", got, want)
	}
}

// Group leads the key, so it decides the order even when the contract
// would sort the other way.
func TestStartOrdersByGroupFirst(t *testing.T) {
	bus, tr := busOver()
	start(t, context.Background(), bus,
		events.Subscription{Event: "a.One", Consumer: "Y", Group: "beta", Handle: noop()},
		events.Subscription{Event: "b.Two", Consumer: "X", Group: "alpha", Handle: noop()},
	)
	want := []events.Group{"alpha", "beta"}
	for i, sub := range tr.subs {
		if sub.Group != want[i] {
			t.Fatalf("registration order = %v, want %v", tr.subs, want)
		}
	}
}

// The transport receives handlers wrapped in a recover.
func TestTheTransportIsHandedWrappedHandlers(t *testing.T) {
	bus, tr := busOver()
	start(t, context.Background(), bus, events.Subscription{
		Event: "a.One", Consumer: "A", Group: "g",
		Handle: func(context.Context, *events.Message) error { panic("boom") },
	})
	err := tr.subs[0].Handle(context.Background(), &events.Message{Event: "a.One"})
	var panicked *events.PanicError
	if !errors.As(err, &panicked) {
		t.Fatalf("the handler handed to the transport is not wrapped in a recover: %v", err)
	}
}

// A registration that fails its checks never reaches the transport.
func TestAnEntryThatFailsItsChecksNeverReachesTheTransport(t *testing.T) {
	tr := &recordingTransport{}
	bus := events.New(events.WithTransport(tr), events.WithCodecFor("a.One", codecjson.Codec{}))
	if err := bus.Register(events.Subscription{Event: "a.One", Consumer: "A", Group: "g", Handle: noop()}); err != nil {
		t.Fatalf("register: %v", err)
	}
	err := bus.Register(events.Subscription{Event: "b.NoCodec", Consumer: "B", Group: "g2", Handle: noop()})
	if !errors.Is(err, events.ErrNoCodec) {
		t.Fatalf("err = %v, want ErrNoCodec", err)
	}
	if err := bus.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if len(tr.subs) != 1 {
		t.Fatalf("the transport was handed %d subscriptions, want the one that passed", len(tr.subs))
	}
}

// Start returns the transport's own refusal.
func TestTheTransportsRefusalIsSurfaced(t *testing.T) {
	bus, tr := busOver()
	tr.err = errors.New("group already registered")
	if err := bus.Register(events.Subscription{Event: "a.One", Consumer: "A", Group: "g", Handle: noop()}); err != nil {
		t.Fatalf("register: %v", err)
	}
	err := bus.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "group already registered") {
		t.Fatalf("err = %v, want the transport's refusal", err)
	}
}

func TestAnEmptyBatchIsNotHandedOver(t *testing.T) {
	bus, tr := busOver()
	if err := bus.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tr.batches != 0 {
		t.Fatal("an empty batch reached the transport")
	}
}

func TestRegisterRefusesASubscriptionWithNoGroup(t *testing.T) {
	bus, _ := busOver()
	err := bus.Register(events.Subscription{Event: "a.One", Consumer: "A", Handle: noop()})
	if !errors.Is(err, events.ErrNoGroup) {
		t.Fatalf("err = %v, want ErrNoGroup", err)
	}
}

func TestRegisterRefusesASubscriptionWithNoHandler(t *testing.T) {
	bus, _ := busOver()
	err := bus.Register(events.Subscription{Event: "a.One", Consumer: "A", Group: "g"})
	if !errors.Is(err, events.ErrNoHandler) {
		t.Fatalf("err = %v, want ErrNoHandler", err)
	}
}

func TestRegisterRefusesAContractWithNoCodec(t *testing.T) {
	tr := &recordingTransport{}
	bus := events.New(events.WithTransport(tr))
	err := bus.Register(events.Subscription{Event: "a.One", Consumer: "A", Group: "g", Handle: noop()})
	if !errors.Is(err, events.ErrNoCodec) {
		t.Fatalf("err = %v, want ErrNoCodec", err)
	}
}

// Register refuses a second subscription to one contract in one group, but not in
// another group.
func TestRegisterRefusesTheSameContractTwiceInOneGroup(t *testing.T) {
	bus, _ := busOver()
	first := events.Subscription{Event: "a.One", Consumer: "A", Group: "g", Handle: noop()}
	if err := bus.Register(first); err != nil {
		t.Fatalf("register: %v", err)
	}
	err := bus.Register(events.Subscription{Event: "a.One", Consumer: "B", Group: "g", Handle: noop()})
	if !errors.Is(err, events.ErrDuplicateSubscription) {
		t.Fatalf("err = %v, want ErrDuplicateSubscription", err)
	}
	if err := bus.Register(events.Subscription{Event: "a.One", Consumer: "B", Group: "other", Handle: noop()}); err != nil {
		t.Fatalf("a second group on one contract was refused: %v", err)
	}
}

// The contracts of the joined-registration tests.
var (
	aOne   = events.NewEvent[order]("a.One", nil)
	bTwo   = events.NewEvent[order]("b.Two", nil)
	cThree = events.NewEvent[order]("c.Three", nil)
)

// ignore is a typed handler that does nothing.
func ignore(context.Context, *order) error { return nil }

// Every joined Subscribe line reaches the transport.
func TestSubscribeRegistersEveryOne(t *testing.T) {
	bus, tr := busOver()
	if err := errors.Join(
		aOne.Subscribe(bus, "g", ignore),
		bTwo.Subscribe(bus, "g", ignore),
	); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := bus.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if len(tr.subs) != 2 {
		t.Fatalf("the transport was handed %d subscriptions, want 2", len(tr.subs))
	}
}

// Joined Subscribe lines report every refusal and still register the lines after one.
func TestJoinedSubscribesReportEveryRefusal(t *testing.T) {
	bus, _ := busOver()
	err := errors.Join(
		aOne.Subscribe(bus, "g", ignore),
		aOne.Subscribe(bus, "g", ignore),
		bTwo.Subscribe(bus, "", ignore),
		cThree.Subscribe(bus, "g", ignore),
	)
	if !errors.Is(err, events.ErrDuplicateSubscription) || !errors.Is(err, events.ErrNoGroup) {
		t.Fatalf("err = %v, want both refusals - the duplicate and the missing group", err)
	}
	var refused *events.RegisterError
	if !errors.As(err, &refused) {
		t.Fatalf("err = %T %v, want *RegisterError", err, err)
	}
	if refused.Event != "a.One" || refused.Group != "g" {
		t.Errorf("the error does not name the contract and group: %+v", refused)
	}
	for _, want := range []string{"a.One", "b.Two"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the joined error does not name %s: %v", want, err)
		}
	}

	var got []string
	for _, group := range bus.Plan().Groups {
		for _, consumer := range group.Consumers {
			got = append(got, consumer.Event)
		}
	}
	if want := "a.One,c.Three"; strings.Join(got, ",") != want {
		t.Errorf("registered %v, want %s - a refusal must not stop the lines after it", got, want)
	}
}

// A *RegisterError names the subscription it refused.
func TestARegisterErrorNamesTheSubscription(t *testing.T) {
	bus, _ := busOver()
	err := bus.Register(events.Subscription{Event: "orders.Placed", Consumer: "SendReceipt", Handle: noop()})
	var refused *events.RegisterError
	if !errors.As(err, &refused) {
		t.Fatalf("err = %T %v, want *RegisterError", err, err)
	}
	if refused.Event != "orders.Placed" || refused.Consumer != "SendReceipt" {
		t.Errorf("the error does not name the subscription: %+v", refused)
	}
	for _, want := range []string{"orders.Placed", "SendReceipt"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q does not name %q", err.Error(), want)
		}
	}
}

func TestASecondStartIsRefused(t *testing.T) {
	bus, tr := busOver()
	start(t, context.Background(), bus, events.Subscription{
		Event: "a.One", Consumer: "A", Group: "g", Handle: noop(),
	})
	if err := bus.Start(context.Background()); !errors.Is(err, events.ErrStarted) {
		t.Fatalf("err = %v, want ErrStarted", err)
	}
	if tr.batches != 1 {
		t.Errorf("the transport was called %d times, want 1", tr.batches)
	}
}

func TestRegisterAfterStartIsRefused(t *testing.T) {
	bus, _ := busOver()
	if err := bus.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	err := bus.Register(events.Subscription{Event: "a.One", Consumer: "A", Group: "g", Handle: noop()})
	if !errors.Is(err, events.ErrStarted) {
		t.Fatalf("err = %v, want ErrStarted", err)
	}
}

// After a failed Start, Start and Register both return ErrStarted.
func TestAFailedStartStillStartedTheBus(t *testing.T) {
	bus, tr := busOver()
	tr.err = errors.New("broker unreachable")
	if err := bus.Register(events.Subscription{Event: "a.One", Consumer: "A", Group: "g", Handle: noop()}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := bus.Start(context.Background()); err == nil {
		t.Fatal("want the transport's failure")
	}
	if err := bus.Start(context.Background()); !errors.Is(err, events.ErrStarted) {
		t.Errorf("a second Start after a failed one = %v, want ErrStarted", err)
	}
	err := bus.Register(events.Subscription{Event: "b.Two", Consumer: "B", Group: "g2", Handle: noop()})
	if !errors.Is(err, events.ErrStarted) {
		t.Errorf("a Register after a failed Start = %v, want ErrStarted", err)
	}
}

// planned registers three consumers over two groups, in neither group nor contract order.
func planned(t *testing.T) *events.Bus {
	t.Helper()
	bus, _ := busOver()
	for _, sub := range []events.Subscription{
		{Event: "orders.Placed", Consumer: "Index", Group: "search", Handle: noop()},
		{Event: "orders.Shipped", Consumer: "Dispatch", Group: "orders-worker", Handle: noop()},
		{Event: "orders.Placed", Consumer: "Receipt", Group: "orders-worker", Handle: noop()},
	} {
		if err := bus.Register(sub); err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	return bus
}

func TestPlanGroupsTheConsumersInAStableOrder(t *testing.T) {
	plan := planned(t).Plan()
	if len(plan.Groups) != 2 {
		t.Fatalf("plan = %+v, want two groups", plan)
	}
	if plan.Groups[0].Name != "orders-worker" || plan.Groups[1].Name != "search" {
		t.Errorf("groups = %v, %v, want them sorted by name", plan.Groups[0].Name, plan.Groups[1].Name)
	}
	worker := plan.Groups[0].Consumers
	if len(worker) != 2 || worker[0].Event != "orders.Placed" || worker[1].Event != "orders.Shipped" {
		t.Errorf("consumers = %+v, want them sorted by contract", worker)
	}
	if worker[0].Consumer != "Receipt" {
		t.Errorf("consumer = %q, want the handler name", worker[0].Consumer)
	}
}

// A plan marshals to one JSON shape and order, whatever the registration order.
func TestPlanMarshalsToAStableShape(t *testing.T) {
	got, err := json.Marshal(planned(t).Plan())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"groups":[` +
		`{"name":"orders-worker","consumers":[` +
		`{"event":"orders.Placed","consumer":"Receipt"},` +
		`{"event":"orders.Shipped","consumer":"Dispatch"}]},` +
		`{"name":"search","consumers":[{"event":"orders.Placed","consumer":"Index"}]}]}`
	if string(got) != want {
		t.Errorf("plan JSON =\n%s\nwant\n%s", got, want)
	}
}

// A hand-built plan out of order marshals sorted too.
func TestAHandBuiltPlanMarshalsInOrderToo(t *testing.T) {
	got, err := json.Marshal(events.Plan{Groups: []events.PlanGroup{
		{Name: "search", Consumers: []events.PlanConsumer{{Event: "a.One", Consumer: "Index"}}},
		{Name: "orders", Consumers: []events.PlanConsumer{
			{Event: "b.Two", Consumer: "Z"},
			{Event: "a.One", Consumer: "A"},
		}},
	}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"groups":[` +
		`{"name":"orders","consumers":[{"event":"a.One","consumer":"A"},{"event":"b.Two","consumer":"Z"}]},` +
		`{"name":"search","consumers":[{"event":"a.One","consumer":"Index"}]}]}`
	if string(got) != want {
		t.Errorf("plan JSON =\n%s\nwant\n%s", got, want)
	}
}

// An empty plan marshals as an empty list.
func TestAnEmptyPlanMarshalsAsAnEmptyList(t *testing.T) {
	bus, _ := busOver()
	got, err := json.Marshal(bus.Plan())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `{"groups":[]}`; string(got) != want {
		t.Errorf("plan JSON = %s, want %s", got, want)
	}
}

// The plan is the same before and after Start.
func TestThePlanIsTheSameBeforeAndAfterStart(t *testing.T) {
	bus := planned(t)
	before, err := json.Marshal(bus.Plan())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := bus.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	after, err := json.Marshal(bus.Plan())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("plan changed at Start:\n%s\n%s", before, after)
	}
}
