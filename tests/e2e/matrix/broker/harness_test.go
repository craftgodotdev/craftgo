// Package broker runs the code craftgo generated for ../design against a
// real broker. It is a package of its own so the matrix's own suite keeps
// building and running without a broker client: the tests here are the
// ones that cannot be written any other way, and they are slow because a
// broker is.
//
// The broker is franz-go's in-process kfake - no Docker, no network.
package broker

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"github.com/twmb/franz-go/pkg/kversion"

	craftevents "github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
	craftkafka "github.com/craftgodotdev/craftgo/pkg/events/kafka"

	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/consumers"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/events/events"
	eventtypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/events"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/svccontext"
)

// The contracts this package addresses by topic, read off the generated
// descriptors rather than retyped. The default mapping is the contract
// name unchanged, so a contract with no topic cannot be published - which
// is how a partial batch failure is produced without a transport written
// to fail.
const (
	itemStocked     = events.ItemStockedContract
	warehouseClosed = events.WarehouseClosedContract
	tierPromoted    = events.TierPromotedContract
	forged          = events.ForgedContract
)

// trackTier is the label the events.TierPromoted listener records its
// deliveries under, which is the reading the disposition test joins on.
const trackTier = "TrackTier"

// planned is what this deployable registers, read off a bus with no
// transport: a subscription's identity is decided at registration, before
// any broker is involved.
func planned(t *testing.T) craftevents.Plan {
	t.Helper()
	bus := craftevents.New(craftevents.WithCodec(codecjson.Codec{}))
	if err := consumers.RegisterAll(bus, svccontext.NewServiceContext()); err != nil {
		t.Fatalf("register consumers: %v", err)
	}
	return bus.Plan()
}

// soleListenerOf fails unless exactly one registered subscription listens
// to contract, and returns its group. The disposition test reads a
// delivery SEQUENCE, which is only unambiguous while one goroutine
// produces it - so the premise is checked against the registration rather
// than written down beside it, where a second listener would leave it
// stale and the test reading interleavings.
//
// A stale group name here would configure a group nobody joins - a share
// group reads from the end of the topic unless its config says otherwise
// - and the redelivery test would then time out after sixty seconds with
// a message a genuine transport regression produces word for word.
// Reading it fails in no time at all, naming itself.
func soleListenerOf(t *testing.T, contract string) craftevents.Group {
	t.Helper()
	var groups []craftevents.Group
	for _, g := range planned(t).Groups {
		for _, c := range g.Consumers {
			if c.Event == contract {
				groups = append(groups, g.Name)
			}
		}
	}
	if len(groups) != 1 {
		t.Fatalf("%d listeners of %s in this deployable, want exactly 1 - a delivery sequence read off several is not a sequence", len(groups), contract)
	}
	return groups[0]
}

// tierGroup is the group this deployable listens to events.TierPromoted in.
func tierGroup(t *testing.T) string {
	t.Helper()
	return string(soleListenerOf(t, tierPromoted))
}

// cluster starts an in-memory broker seeding exactly the topics named.
func cluster(t *testing.T, topics ...string) []string {
	t.Helper()
	c, err := kfake.NewCluster(
		kfake.NumBrokers(1),
		kfake.SeedTopics(1, topics...),
		// A capability floor, not an arbitrary version, and the only
		// place this package states one. Serving the three share APIs is
		// not enough: AckRenew is a ShareAcknowledge v2 field, so the
		// adapter's probe refuses any broker below that while lock
		// renewal is on. Lowering this turns every share test here into a
		// startup failure rather than a slower run.
		kfake.MaxVersions(kversion.V4_2_0()),
	)
	if err != nil {
		t.Fatalf("start broker: %v", err)
	}
	t.Cleanup(c.Close)
	return c.ListenAddrs()
}

// createTopic adds a topic to a running broker, so a test can publish
// while it is missing and consume once it is there.
func createTopic(t *testing.T, addrs []string, topic string) {
	t.Helper()
	cl, err := kgo.NewClient(kgo.SeedBrokers(addrs...))
	if err != nil {
		t.Fatalf("open admin client: %v", err)
	}
	defer cl.Close()

	req := kmsg.NewPtrCreateTopicsRequest()
	rt := kmsg.NewCreateTopicsRequestTopic()
	rt.Topic, rt.NumPartitions, rt.ReplicationFactor = topic, 1, 1
	req.Topics = append(req.Topics, rt)
	resp, err := req.RequestWith(context.Background(), cl)
	if err != nil {
		t.Fatalf("create topic %s: %v", topic, err)
	}
	for _, r := range resp.Topics {
		if err := kerr.ErrorForCode(r.ErrorCode); err != nil {
			t.Fatalf("create topic %s: %v", topic, err)
		}
	}
}

// shareFromEarliest opts a share group into reading what is already on
// the topic; one otherwise starts at the end.
func shareFromEarliest(t *testing.T, addrs []string, group string) {
	t.Helper()
	cl, err := kgo.NewClient(kgo.SeedBrokers(addrs...))
	if err != nil {
		t.Fatalf("open admin client: %v", err)
	}
	defer cl.Close()

	req := kmsg.NewPtrIncrementalAlterConfigsRequest()
	res := kmsg.NewIncrementalAlterConfigsRequestResource()
	res.ResourceType, res.ResourceName = kmsg.ConfigResourceTypeGroupConfig, group
	cfg := kmsg.NewIncrementalAlterConfigsRequestResourceConfig()
	cfg.Name, cfg.Value = "share.auto.offset.reset", kmsg.StringPtr("earliest")
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

// reported collects what a transport said about a delivery. The report
// arrives on the transport's own read goroutine, and Close does not join
// that goroutine - so logging from there races the end of the test, which
// ends the whole binary in a panic. Rows are held and printed from the
// test goroutine instead.
type reported struct {
	mu   sync.Mutex
	rows []string
}

func (r *reported) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = append(r.rows, s)
}

func (r *reported) dump(t *testing.T) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, row := range r.rows {
		t.Logf("transport reported: %s", row)
	}
}

// boot wires this deployable's subscriptions onto a Kafka transport.
// subscribe is false for a publish-only test, so nothing listens to a
// topic that is deliberately missing.
func boot(t *testing.T, addrs []string, subscribe bool, tropts []craftkafka.Option, busopts []craftevents.Option) (*svccontext.ServiceContext, *craftevents.Bus) {
	t.Helper()
	// Registered first, so it runs last: after the read loops are
	// cancelled and the transport is closed, with everything they said
	// already collected.
	said := &reported{}
	t.Cleanup(func() { said.dump(t) })

	tropts = append([]craftkafka.Option{
		craftkafka.WithErrorHandler(func(_ craftevents.Subscription, _ *craftevents.Message, err error) {
			said.add(err.Error())
		}),
		// A record for a missing topic is retried before the batch
		// reports. Against a real broker that runs to the delivery
		// timeout; against kfake it costs about a second, which is worth
		// not paying on every run. Nothing here asserts the option - the
		// rule that a caller option reaches the client is the adapter's,
		// and pkg/events/kafka pins it.
		craftkafka.WithClientOptions(kgo.UnknownTopicRetries(0)),
	}, tropts...)
	tr := craftkafka.New(addrs, tropts...)
	t.Cleanup(func() { _ = tr.Close() })

	bus := craftevents.New(append([]craftevents.Option{
		craftevents.WithTransport(tr),
		craftevents.WithCodec(codecjson.Codec{}),
	}, busopts...)...)
	svc := svccontext.NewServiceContext()
	if subscribe {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		if err := consumers.RegisterAll(bus, svc); err != nil {
			t.Fatalf("register consumers: %v", err)
		}
		if err := bus.Start(ctx); err != nil {
			t.Fatalf("start consumers: %v", err)
		}
	}
	return svc, bus
}

// counts renders how many payloads each listener has been handed, so a
// test joins on a reading it can also print when the reading is wrong.
func counts(svc *svccontext.ServiceContext, names ...string) func() string {
	return func() string {
		parts := make([]string, 0, len(names))
		for _, c := range names {
			parts = append(parts, fmt.Sprintf("%s=%d", c, len(svc.DeliveredTo(c))))
		}
		return strings.Join(parts, " ")
	}
}

// waitFor blocks until state reads want, so a broker test joins on an
// outcome rather than sleeping for one.
//
// A timeout names the last reading. Without it a redelivery that never
// came, a consumer group that drifted out of the deployable, and a bus
// that lost the ask all end the same sixty seconds of silence, and the
// message is the only thing that could have told them apart.
func waitFor(t *testing.T, within time.Duration, what, want string, state func() string) {
	t.Helper()
	deadline := time.Now().Add(within)
	last := state()
	for time.Now().Before(deadline) {
		if last = state(); last == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s: last saw %s, want %s", what, last, want)
}

// stillTrue asserts state reads want for the WHOLE window, which is how
// "and nothing more arrived" is proved. It polls, so a reading that moves
// and moves back inside the window is caught rather than slept through.
func stillTrue(t *testing.T, within time.Duration, complaint, want string, state func() string) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if got := state(); got != want {
			t.Errorf("%s: saw %s, want %s", complaint, got, want)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// skus renders what a listener was handed, so a test names the entries
// that reached a broker rather than counting them.
func skus(payloads []any) []string {
	out := make([]string, 0, len(payloads))
	for _, p := range payloads {
		if v, ok := p.(*eventtypes.ItemStocked); ok {
			out = append(out, v.Sku)
			continue
		}
		out = append(out, fmt.Sprintf("%#v", p))
	}
	sort.Strings(out)
	return out
}
