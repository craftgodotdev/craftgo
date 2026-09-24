// Package broker runs the code craftgo generated for ../design against
// kfake, franz-go's in-process Kafka broker.
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

// Each contract travels on a topic of its own name, so one whose topic the
// cluster lacks fails to publish.
const (
	itemStocked     = events.ItemStockedContract
	warehouseClosed = events.WarehouseClosedContract
	tierPromoted    = events.TierPromotedContract
	forged          = events.ForgedContract
)

// trackTier is what the events.TierPromoted listener records under.
const trackTier = "TrackTier"

// planned returns the plan of consumers.Register on a bus with no transport.
func planned(t *testing.T) craftevents.Plan {
	t.Helper()
	bus := craftevents.New(craftevents.WithCodec(codecjson.Codec{}))
	if err := consumers.Register(bus, svccontext.NewServiceContext()); err != nil {
		t.Fatalf("register consumers: %v", err)
	}
	return bus.Plan()
}

// soleListenerOf returns the group of the only subscription to contract,
// failing unless there is exactly one.
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
		// The floor for share groups with lock renewal: the adapter needs
		// ShareAcknowledge v2 (AckRenew), which is Kafka 4.2.
		kfake.MaxVersions(kversion.V4_2_0()),
	)
	if err != nil {
		t.Fatalf("start broker: %v", err)
	}
	t.Cleanup(c.Close)
	return c.ListenAddrs()
}

// createTopic creates topic on a running broker.
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

// shareFromEarliest makes group read what the topic already holds; a share
// group starts at the end by default.
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

// reported holds transport errors for dump: they arrive on a read goroutine
// Close does not join, and t.Logf after the test ends panics.
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

// boot builds a bus on a Kafka transport; with subscribe it also registers
// and starts this deployable's consumers.
func boot(t *testing.T, addrs []string, subscribe bool, tropts []craftkafka.Option, busopts []craftevents.Option) (*svccontext.ServiceContext, *craftevents.Bus) {
	t.Helper()
	// Registered first, so it runs last, after the transport is closed.
	said := &reported{}
	t.Cleanup(func() { said.dump(t) })

	tropts = append([]craftkafka.Option{
		craftkafka.WithErrorHandler(func(_ craftevents.Subscription, _ *craftevents.Message, err error) {
			said.add(err.Error())
		}),
		// A record for a missing topic fails without retries.
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
		if err := consumers.Register(bus, svc); err != nil {
			t.Fatalf("register consumers: %v", err)
		}
		if err := bus.Start(ctx); err != nil {
			t.Fatalf("start consumers: %v", err)
		}
	}
	return svc, bus
}

// counts returns a reading of each named listener's payload count.
func counts(svc *svccontext.ServiceContext, names ...string) func() string {
	return func() string {
		parts := make([]string, 0, len(names))
		for _, c := range names {
			parts = append(parts, fmt.Sprintf("%s=%d", c, len(svc.DeliveredTo(c))))
		}
		return strings.Join(parts, " ")
	}
}

// waitFor polls state until it reads want, failing with the last reading
// once within has passed.
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

// stillTrue polls state for the whole window and fails on the first reading
// other than want.
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

// skus returns the sorted SKUs of ItemStocked payloads, and %#v of the rest.
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
