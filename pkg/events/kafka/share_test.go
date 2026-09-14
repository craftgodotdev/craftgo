package kafka

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"github.com/twmb/franz-go/pkg/kversion"

	events "github.com/craftgodotdev/craftgo/pkg/events"
)

// cluster starts an in-memory broker serving at most the given Kafka
// release, so a test can say what the broker is capable of.
func cluster(t *testing.T, topic string, max *kversion.Versions) []string {
	t.Helper()
	c, err := kfake.NewCluster(
		kfake.NumBrokers(1),
		kfake.SeedTopics(1, topic),
		kfake.MaxVersions(max),
	)
	if err != nil {
		t.Fatalf("start broker: %v", err)
	}
	t.Cleanup(c.Close)
	return c.ListenAddrs()
}

// shareFromEarliest opts the group into reading what is already on the
// topic. A share group defaults to "latest", so without this a test that
// produces before subscribing sees nothing.
func shareFromEarliest(t *testing.T, addrs []string, group string) {
	t.Helper()
	cl, err := kgo.NewClient(kgo.SeedBrokers(addrs...))
	if err != nil {
		t.Fatalf("open admin client: %v", err)
	}
	defer cl.Close()

	req := kmsg.NewPtrIncrementalAlterConfigsRequest()
	res := kmsg.NewIncrementalAlterConfigsRequestResource()
	res.ResourceType = kmsg.ConfigResourceTypeGroupConfig
	res.ResourceName = group
	cfg := kmsg.NewIncrementalAlterConfigsRequestResourceConfig()
	cfg.Name = "share.auto.offset.reset"
	cfg.Value = kmsg.StringPtr("earliest")
	res.Configs = append(res.Configs, cfg)
	req.Resources = append(req.Resources, res)

	resp, err := req.RequestWith(context.Background(), cl)
	if err != nil {
		t.Fatalf("set share.auto.offset.reset: %v", err)
	}
	for _, r := range resp.Resources {
		if err := kerr.ErrorForCode(r.ErrorCode); err != nil {
			t.Fatalf("set share.auto.offset.reset: %v", err)
		}
	}
}

// deliveries collects what a subscription was handed, so a test can wait
// for a count rather than sleep for a duration.
type deliveries struct {
	mu   sync.Mutex
	got  []*events.Message
	seen chan struct{}
}

func newDeliveries() *deliveries {
	return &deliveries{seen: make(chan struct{}, 256)}
}

func (d *deliveries) add(msg *events.Message) {
	d.mu.Lock()
	d.got = append(d.got, msg)
	d.mu.Unlock()
	select {
	case d.seen <- struct{}{}:
	default:
	}
}

func (d *deliveries) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.got)
}

// waitFor blocks until n deliveries have arrived, or fails the test.
func (d *deliveries) waitFor(t *testing.T, n int) {
	t.Helper()
	deadline := time.After(30 * time.Second)
	for d.count() < n {
		select {
		case <-d.seen:
		case <-deadline:
			t.Fatalf("saw %d deliveries, want %d", d.count(), n)
		}
	}
}

// quiet asserts no further delivery arrives, which is how "the loop
// ended" is proved.
func (d *deliveries) quiet(t *testing.T, within time.Duration, want int) {
	t.Helper()
	time.Sleep(within)
	if got := d.count(); got != want {
		t.Errorf("deliveries = %d, want %d - nothing more should have arrived", got, want)
	}
}

// publish sends one message through the transport under test.
func publish(t *testing.T, tr *Transport, contract, key string, body []byte) {
	t.Helper()
	if err := tr.Publish(context.Background(), &events.Message{
		Event: contract, Key: key, Payload: body,
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

// The probe is the difference between a refusal at startup and a
// deployable that boots, serves HTTP, passes readiness and consumes
// nothing. franz-go reports a missing share API on the first poll, which
// happens on a goroutine nobody is waiting on.
func TestSubscribeRefusesAShareGroupTheBrokerCannotServe(t *testing.T) {
	const contract = "orders.Placed"
	cases := []struct {
		release string
		max     *kversion.Versions
		serves  bool
	}{
		{"4.2", kversion.V4_2_0(), true},
		{"4.1", kversion.V4_1_0(), true},
		{"4.0", kversion.V4_0_0(), false},
		{"3.9", kversion.V3_9_0(), false},
	}
	for _, c := range cases {
		t.Run(c.release, func(t *testing.T) {
			tr := New(cluster(t, contract, c.max), WithShareGroup())
			defer func() { _ = tr.Close() }()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			err := tr.Subscribe(ctx, []events.Subscription{{
				Event: contract, Consumer: "C", Group: events.Group("g-" + c.release),
				Handle: func(context.Context, *events.Message) error { return nil },
			}})

			if c.serves {
				// 4.1 serves every share key; what it cannot do is renew a
				// lock, which is asserted separately.
				if err != nil && !strings.Contains(err.Error(), "WithLockRenewInterval") {
					t.Fatalf("Kafka %s serves the share APIs: %v", c.release, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Kafka %s does not serve the share APIs and must be refused", c.release)
			}
			for _, want := range []string{"WithShareGroup", "4.1", "classic consumer group"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal does not mention %q: %v", want, err)
				}
			}
		})
	}
}

// A refused subscription must not leave its claim behind, or the group
// would be unusable for anything else afterwards.
func TestARefusedSubscriptionReleasesItsClaim(t *testing.T) {
	const contract = "orders.Placed"
	tr := New(cluster(t, contract, kversion.V3_9_0()), WithShareGroup())
	defer func() { _ = tr.Close() }()

	sub := events.Subscription{
		Event: contract, Consumer: "C", Group: "g",
		Handle: func(context.Context, *events.Message) error { return nil },
	}
	if err := tr.Subscribe(context.Background(), []events.Subscription{sub}); err == nil {
		t.Fatal("expected a refusal")
	}
	if err := tr.claim("g", contract, "orders.Cancelled"); err != nil {
		t.Errorf("the refused subscription kept its claim: %v", err)
	}
}

// Release hands the same record back; reject gives it up. This is the
// whole reason the share mode exists.
func TestReleaseRedeliversAndRejectGivesUp(t *testing.T) {
	const (
		contract = "orders.Placed"
		group    = "redeliver"
	)
	addrs := cluster(t, contract, kversion.V4_2_0())
	shareFromEarliest(t, addrs, group)

	tr := New(addrs, WithShareGroup(), WithMaxDeliveries(0))
	defer func() { _ = tr.Close() }()
	publish(t, tr, contract, "o-1", []byte(`{"id":1}`))

	got := newDeliveries()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: contract, Consumer: "C", Group: group,
		Handle: func(_ context.Context, msg *events.Message) error {
			got.add(msg)
			// Ask for it back once, then give it up: the broker must
			// produce the same record both times and nothing after.
			if msg.Deliveries() <= 1 {
				msg.Redeliver()
			} else {
				msg.Reject()
			}
			return nil
		},
	}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	got.waitFor(t, 2)
	got.quiet(t, 2*time.Second, 2)

	got.mu.Lock()
	defer got.mu.Unlock()
	for i, msg := range got.got {
		if msg.Key != "o-1" || string(msg.Payload) != `{"id":1}` {
			t.Errorf("delivery %d is a different record: %+v", i, msg)
		}
	}
	if got.got[0].Deliveries() != 1 || got.got[1].Deliveries() != 2 {
		t.Errorf("delivery counts = %d, %d - want 1 then 2",
			got.got[0].Deliveries(), got.got[1].Deliveries())
	}
}

// A middleware that keeps asking for a record nothing can handle is a
// loop, and the cap is what ends it.
func TestMaxDeliveriesTerminatesARedeliveryLoop(t *testing.T) {
	const (
		contract = "orders.Placed"
		group    = "capped"
		cap      = 3
	)
	addrs := cluster(t, contract, kversion.V4_2_0())
	shareFromEarliest(t, addrs, group)

	tr := New(addrs, WithShareGroup(), WithMaxDeliveries(cap))
	defer func() { _ = tr.Close() }()
	publish(t, tr, contract, "o-1", []byte(`{}`))

	got := newDeliveries()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: contract, Consumer: "C", Group: group,
		Handle: func(_ context.Context, msg *events.Message) error {
			got.add(msg)
			msg.Redeliver() // never gives up
			return nil
		},
	}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	got.waitFor(t, cap)
	got.quiet(t, 3*time.Second, cap)
}

// The runtime clears a disposition a panicking frame asked for, and the
// adapter has to read cleared as "take it". Without both halves a
// middleware that asked for redelivery and then panicked would be
// redelivered for ever with nothing left to stop it.
func TestAPanicAfterAskingForRedeliveryDoesNotLoop(t *testing.T) {
	const (
		contract = "orders.Placed"
		group    = "panicker"
	)
	addrs := cluster(t, contract, kversion.V4_2_0())
	shareFromEarliest(t, addrs, group)

	tr := New(addrs, WithShareGroup(), WithMaxDeliveries(0))
	defer func() { _ = tr.Close() }()
	publish(t, tr, contract, "o-1", []byte(`{}`))

	got := newDeliveries()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The bus supplies the recover that clears the flag, which is the
	// half under test on the runtime's side.
	bus := events.New(
		events.WithTransport(tr),
		events.WithCodec(rawCodec{}),
		events.WithMiddleware(func(_ events.Subscription, next events.Handler) events.Handler {
			return func(ctx context.Context, msg *events.Message) error {
				msg.Redeliver()
				return next(ctx, msg)
			}
		}),
	)
	if err := bus.Register(events.Subscription{
		Event: contract, Consumer: "C", Group: group,
		Handle: func(_ context.Context, msg *events.Message) error {
			got.add(msg)
			panic("handler exploded")
		},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := bus.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	got.waitFor(t, 1)
	got.quiet(t, 3*time.Second, 1)
}

// A chain that needs redelivery must not be wired onto a classic group,
// where Redeliver silently settles. The refusal is at registration,
// before anything is handed to the broker.
func TestARequiredDispositionFailsOnAClassicGroup(t *testing.T) {
	const contract = "orders.Placed"
	addrs := cluster(t, contract, kversion.V4_2_0())

	tr := New(addrs)
	defer func() { _ = tr.Close() }()
	bus := events.New(
		events.WithTransport(tr),
		events.WithCodec(rawCodec{}),
		events.WithDispositionRequired(events.DispositionRedeliver),
	)
	err := bus.Register(events.Subscription{
		Event: contract, Consumer: "C", Group: "g",
		Handle: func(context.Context, *events.Message) error { return nil },
	})
	if !errors.Is(err, events.ErrDispositionUnsupported) {
		t.Fatalf("register = %v, want ErrDispositionUnsupported - a classic group cannot redeliver", err)
	}
}

// rawCodec is the identity codec: these tests care about delivery, not
// encoding, and a payload that is not JSON must still reach a handler.
type rawCodec struct{}

func (rawCodec) Name() string { return "raw" }

func (rawCodec) Marshal(v any) ([]byte, error) {
	if b, ok := v.([]byte); ok {
		return b, nil
	}
	return nil, nil
}

func (rawCodec) Unmarshal([]byte, any) error { return nil }

// The classic mode is the default and has to keep working: a record
// reaches the handler and the group's offset advances past it.
func TestAClassicGroupDelivers(t *testing.T) {
	const (
		contract = "orders.Placed"
		group    = "classic"
	)
	addrs := cluster(t, contract, kversion.V4_2_0())

	tr := New(addrs)
	defer func() { _ = tr.Close() }()

	got := newDeliveries()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: contract, Consumer: "C", Group: group,
		Handle: func(_ context.Context, msg *events.Message) error {
			got.add(msg)
			return nil
		},
	}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	publish(t, tr, contract, "o-1", []byte(`{"id":1}`))

	got.waitFor(t, 1)
	got.mu.Lock()
	defer got.mu.Unlock()
	if got.got[0].Key != "o-1" {
		t.Errorf("delivered %+v", got.got[0])
	}
	// A classic group has no per-record delivery count to report.
	if n := got.got[0].Deliveries(); n != 0 {
		t.Errorf("deliveries = %d, want 0 - a classic group does not count", n)
	}
}

// A batch is registered whole. One Subscribe call carrying two
// subscriptions leaves both reading - each joins its own group, so the
// record reaches both rather than being divided between them.
func TestOneSubscribeCallRegistersEverySubscriptionInTheBatch(t *testing.T) {
	const contract = "orders.Placed"
	tr := New(cluster(t, contract, kversion.V4_2_0()))
	defer func() { _ = tr.Close() }()

	reader := map[events.Group]*deliveries{"batch-a": newDeliveries(), "batch-b": newDeliveries()}
	handle := func(got *deliveries) events.Handler {
		return func(_ context.Context, msg *events.Message) error {
			got.add(msg)
			return nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, []events.Subscription{
		{Event: contract, Consumer: "A", Group: "batch-a", Handle: handle(reader["batch-a"])},
		{Event: contract, Consumer: "B", Group: "batch-b", Handle: handle(reader["batch-b"])},
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	publish(t, tr, contract, "o-1", []byte(`{"id":1}`))

	for group, got := range reader {
		got.waitFor(t, 1)
		got.mu.Lock()
		if got.got[0].Key != "o-1" || got.got[0].Event != contract {
			t.Errorf("group %q was handed %+v", group, got.got[0])
		}
		got.mu.Unlock()
	}
}

// A topic carrying a contract this subscription does not consume is a
// mapping mistake, and it used to be taken as done in silence with the
// error handler called zero times. It is reported now.
func TestAForeignContractOnTheTopicIsReported(t *testing.T) {
	const (
		mine   = "orders.Placed"
		theirs = "orders.Cancelled"
		shared = "orders"
		group  = "foreign"
	)
	addrs := cluster(t, shared, kversion.V4_2_0())

	var (
		mu       sync.Mutex
		reported []error
	)
	tr := New(addrs,
		WithTopic(func(string) string { return shared }),
		WithErrorHandler(func(_ events.Subscription, _ *events.Message, err error) {
			mu.Lock()
			reported = append(reported, err)
			mu.Unlock()
		}))
	defer func() { _ = tr.Close() }()

	got := newDeliveries()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: mine, Consumer: "C", Group: group,
		Handle: func(_ context.Context, msg *events.Message) error {
			got.add(msg)
			return nil
		},
	}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	publish(t, tr, theirs, "o-2", []byte(`{}`))
	publish(t, tr, mine, "o-1", []byte(`{}`))

	// The one this subscription asked for arrives; the other does not.
	got.waitFor(t, 1)
	got.mu.Lock()
	if len(got.got) != 1 || got.got[0].Event != mine {
		t.Errorf("handler was given %+v, want only %s", got.got, mine)
	}
	got.mu.Unlock()

	mu.Lock()
	defer mu.Unlock()
	if len(reported) == 0 {
		t.Fatal("the skipped record was not reported - it used to be dropped in silence")
	}
	if !strings.Contains(reported[0].Error(), theirs) || !strings.Contains(reported[0].Error(), "skipped") {
		t.Errorf("report does not name the skipped contract: %v", reported[0])
	}
}

// A middleware reaching for what events.Message does not carry - the
// partition, the offset, the record timestamp - gets the record the
// delivery came from.
func TestTheRecordIsReachableFromADelivery(t *testing.T) {
	const contract = "orders.Placed"
	addrs := cluster(t, contract, kversion.V4_2_0())

	tr := New(addrs)
	defer func() { _ = tr.Close() }()

	type seen struct {
		topic  string
		key    string
		offset int64
		found  bool
	}
	var (
		mu  sync.Mutex
		got seen
	)
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: contract, Consumer: "C", Group: "raw",
		Handle: func(hctx context.Context, _ *events.Message) error {
			mu.Lock()
			if rec, ok := RecordFrom(hctx); ok {
				got = seen{topic: rec.Topic, key: string(rec.Key), offset: rec.Offset, found: true}
			}
			mu.Unlock()
			close(done)
			return nil
		},
	}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	publish(t, tr, contract, "o-1", []byte(`{"id":1}`))

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("no delivery")
	}
	mu.Lock()
	defer mu.Unlock()
	if !got.found {
		t.Fatal("RecordFrom found no record on a Kafka delivery")
	}
	if got.topic != contract || got.key != "o-1" {
		t.Errorf("record = topic %q key %q, want %q / o-1", got.topic, got.key, contract)
	}
	if got.offset < 0 {
		t.Errorf("offset = %d - the record is not the delivered one", got.offset)
	}
}

// A caller option that would change what a client IS must fail
// construction, at EVERY site - not just the consumer. A producer or a
// probe that quietly joined a consumer group would be a second member
// splitting the stream, outside the claim guard that exists to stop
// exactly that, and a probe joining for a moment rebalances the live
// group.
func TestAGroupOptionFromAClientOptionIsRefusedAtEverySite(t *testing.T) {
	const contract = "orders.Placed"
	addrs := cluster(t, contract, kversion.V4_2_0())

	for _, c := range []struct {
		name string
		opt  kgo.Opt
	}{
		{"consumer group", kgo.ConsumerGroup("sneaky")},
		{"share group", kgo.ShareGroup("sneaky")},
		{"consume topics", kgo.ConsumeTopics("sneaky")},
	} {
		t.Run(c.name, func(t *testing.T) {
			tr := New(addrs, WithClientOptions(c.opt))
			defer func() { _ = tr.Close() }()

			// The producer must not consume.
			err := tr.Publish(context.Background(), &events.Message{
				Event: contract, Payload: []byte(`{}`),
			})
			if err == nil {
				t.Fatal("the producer was opened with a consuming identity")
			}
			if !strings.Contains(err.Error(), "the transport's to decide") {
				t.Errorf("error does not say whose decision it is: %v", err)
			}

			// And neither must the share-API probe.
			share := New(addrs, WithShareGroup(), WithClientOptions(c.opt))
			defer func() { _ = share.Close() }()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if err := share.Subscribe(ctx, []events.Subscription{{
				Event: contract, Consumer: "C", Group: "real",
				Handle: func(context.Context, *events.Message) error { return nil },
			}}); err == nil {
				t.Error("the probe was opened with a consuming identity")
			}
		})
	}
}

// The legitimate consumer passes its OWN group and is checked against it
// rather than excused from the check, which is what stops a fourth
// construction site inheriting no check at all.
func TestTheRealConsumerPassesTheSameGuard(t *testing.T) {
	const contract = "orders.Placed"
	tr := New(cluster(t, contract, kversion.V4_2_0()))
	defer func() { _ = tr.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: contract, Consumer: "C", Group: "real",
		Handle: func(context.Context, *events.Message) error { return nil },
	}}); err != nil {
		t.Fatalf("a consumer joining its own group must be allowed: %v", err)
	}
}

// A client option that does NOT touch the consuming identity is passed
// through, which is the point of the option.
func TestAnOrdinaryClientOptionIsPassedThrough(t *testing.T) {
	const contract = "orders.Placed"
	tr := New(cluster(t, contract, kversion.V4_2_0()), WithClientOptions(kgo.ClientID("mine")))
	defer func() { _ = tr.Close() }()

	if err := tr.Publish(context.Background(), &events.Message{
		Event: contract, Payload: []byte(`{}`),
	}); err != nil {
		t.Fatalf("an ordinary option must not fail construction: %v", err)
	}
}

// Kafka 4.1 serves every share key, so a presence check lets it through -
// but renewal rides on ShareAcknowledge v2, and franz-go sets the renew
// flag whatever the negotiated version is. On 4.1 the flag is simply not
// on the wire, so the lock lapses under a slow handler and nothing
// reports it. The floor is asserted on the VERSION, and only when
// renewal is on.
func TestSubscribeRefusesABrokerThatCannotRenewTheLock(t *testing.T) {
	const contract = "orders.Placed"
	addrs := cluster(t, contract, kversion.V4_1_0())

	tr := New(addrs, WithShareGroup())
	defer func() { _ = tr.Close() }()
	err := tr.Subscribe(context.Background(), []events.Subscription{{
		Event: contract, Consumer: "C", Group: "g",
		Handle: func(context.Context, *events.Message) error { return nil },
	}})
	if err == nil {
		t.Fatal("subscribed to a broker that cannot renew a lock")
	}
	for _, want := range []string{"WithLockRenewInterval", "ShareAcknowledge v2", "v1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q", err, want)
		}
	}
}

// The refusal is the renewal's, not share mode's: 4.1 serves share groups
// and honours release and reject, which are v1 ack types. A deployment
// that turns renewal off gets them.
func TestABrokerWithoutRenewalStillServesShareModeWithoutIt(t *testing.T) {
	const contract = "orders.Placed"
	addrs := cluster(t, contract, kversion.V4_1_0())
	shareFromEarliest(t, addrs, "no-renew")

	tr := New(addrs, WithShareGroup(), WithLockRenewInterval(0))
	defer func() { _ = tr.Close() }()
	publish(t, tr, contract, "o-1", []byte(`{}`))

	got := newDeliveries()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: contract, Consumer: "C", Group: "no-renew",
		Handle: func(_ context.Context, msg *events.Message) error {
			got.add(msg)
			msg.Redeliver()
			return nil
		},
	}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	// Release is a v1 ack type, so the record comes back on 4.1.
	got.waitFor(t, 2)
}
