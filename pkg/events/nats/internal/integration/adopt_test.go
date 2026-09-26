package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	events "github.com/craftgodotdev/craftgo/pkg/events"
	craftnats "github.com/craftgodotdev/craftgo/pkg/events/nats"
)

// adoptGroup is the durable every adoption case works on.
const adoptGroup events.Group = "adopting"

// filtersOf returns a durable's subjects from whichever field holds them.
func filtersOf(cfg jetstream.ConsumerConfig) []string {
	if len(cfg.FilterSubjects) > 0 {
		out := append([]string(nil), cfg.FilterSubjects...)
		sort.Strings(out)
		return out
	}
	if cfg.FilterSubject != "" {
		return []string{cfg.FilterSubject}
	}
	return nil
}

// filterFor writes one subject to the single field, several to the list.
func filterFor(cfg *jetstream.ConsumerConfig, subjects []string) {
	if len(subjects) == 1 {
		cfg.FilterSubject = subjects[0]
		return
	}
	cfg.FilterSubjects = subjects
}

// adoptWith subscribes planned over a durable filtering carried.
func adoptWith(t *testing.T, carried, planned []string, opts ...craftnats.JetStreamOption) ([]string, error) {
	t.Helper()
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")

	cfg := jetstream.ConsumerConfig{
		Durable:   string(adoptGroup),
		Name:      string(adoptGroup),
		AckPolicy: jetstream.AckExplicitPolicy,
	}
	filterFor(&cfg, carried)
	createConsumer(t, conn, cfg)

	subs := make([]events.Subscription, 0, len(planned))
	for i, contract := range planned {
		subs = append(subs, events.Subscription{
			Event: contract, Consumer: fmt.Sprintf("C%d", i), Group: adoptGroup,
			Handle: recording(&deliveries{}),
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	err := jsTransport(t, conn, opts...).Subscribe(ctx, subs)
	return filtersOf(consumerConfig(t, conn, string(adoptGroup))), err
}

// A durable already filtering the plan is adopted unchanged.
func TestADurableFilteringThePlanIsAdopted(t *testing.T) {
	both := []string{"orders.Placed", "orders.Shipped"}
	got, err := adoptWith(t, both, both)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if strings.Join(got, ",") != strings.Join(both, ",") {
		t.Errorf("filter = %v, want %v untouched", got, both)
	}
}

// A plan that adds a subject widens the durable's filter.
func TestAWiderPlanRepointsTheDurable(t *testing.T) {
	got, err := adoptWith(t, []string{"orders.Placed"}, []string{"orders.Placed", "orders.Shipped"})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if want := "orders.Placed,orders.Shipped"; strings.Join(got, ",") != want {
		t.Errorf("filter = %v, want %s", got, want)
	}
}

// A plan that drops a subject is refused and leaves the durable unchanged.
func TestANarrowerPlanIsRefused(t *testing.T) {
	carried := []string{"orders.Placed", "orders.Shipped"}
	got, err := adoptWith(t, carried, []string{"orders.Placed"})
	if err == nil {
		t.Fatal("narrowing a durable craftgo did not create must be refused")
	}
	for _, want := range []string{"orders.Placed", "orders.Shipped", "AllowNarrow", string(adoptGroup)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q: %v", want, err)
		}
	}
	if strings.Join(got, ",") != strings.Join(carried, ",") {
		t.Errorf("filter = %v, want %v - a refused plan changes nothing", got, carried)
	}
}

// AllowNarrow lets a narrower plan re-point the durable.
func TestANarrowerPlanIsWrittenWhenTheGroupAllowsIt(t *testing.T) {
	got, err := adoptWith(t,
		[]string{"orders.Placed", "orders.Shipped"}, []string{"orders.Placed"},
		craftnats.WithGroupConfig(adoptGroup, craftnats.AllowNarrow()))
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if len(got) != 1 || got[0] != "orders.Placed" {
		t.Errorf("filter = %v, want the plan", got)
	}
}

// A plan that only partly overlaps the durable's filter is refused.
func TestAPartlyOverlappingPlanIsRefused(t *testing.T) {
	_, err := adoptWith(t,
		[]string{"orders.Placed", "orders.Shipped"}, []string{"orders.Placed", "orders.Paid"})
	if err == nil {
		t.Fatal("a plan that only partly overlaps must be refused")
	}
	if !strings.Contains(err.Error(), "orders.Paid") {
		t.Errorf("refusal does not print the plan: %v", err)
	}
}

// A durable with no filter takes every subject, so any plan narrows it.
func TestADurableWithNoFilterIsRefused(t *testing.T) {
	_, err := adoptWith(t, nil, []string{"orders.Placed"})
	if err == nil {
		t.Fatal("a durable filtering everything must not be narrowed silently")
	}
	if !strings.Contains(err.Error(), "every subject") {
		t.Errorf("refusal does not say what the durable filters: %v", err)
	}

	got, err := adoptWith(t, nil, []string{"orders.Placed"},
		craftnats.WithGroupConfig(adoptGroup, craftnats.AllowNarrow()))
	if err != nil {
		t.Fatalf("subscribe with AllowNarrow: %v", err)
	}
	if len(got) != 1 || got[0] != "orders.Placed" {
		t.Errorf("filter = %v, want the plan", got)
	}
}

// logBuffer collects what the transport logged.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func (l *logBuffer) logger() craftnats.JetStreamOption {
	return craftnats.WithJetStreamLogger(slog.New(slog.NewTextHandler(l, &slog.HandlerOptions{Level: slog.LevelInfo})))
}

func TestCreatingADurableIsLoggedWithItsConfig(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	log := &logBuffer{}
	tr := jsTransport(t, conn, log.logger(), craftnats.WithAckWait(7*time.Second))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: "orders.Placed", Consumer: "C", Group: "fresh", Handle: recording(&deliveries{}),
	}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	line := log.String()
	for _, want := range []string{"created jetstream consumer", "group=fresh", "stream=ORDERS", "orders.Placed", "ack_wait=7s", "max_in_flight=1"} {
		if !strings.Contains(line, want) {
			t.Errorf("log %q does not carry %q", line, want)
		}
	}
}

func TestAdoptingADurableUnchangedLogsNothing(t *testing.T) {
	log := &logBuffer{}
	both := []string{"orders.Placed", "orders.Shipped"}
	if _, err := adoptWith(t, both, both, log.logger()); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if line := log.String(); line != "" {
		t.Errorf("adopting a durable unchanged logged %q", line)
	}
}

func TestRepointingADurableIsLogged(t *testing.T) {
	log := &logBuffer{}
	_, err := adoptWith(t, []string{"orders.Placed"}, []string{"orders.Placed", "orders.Shipped"}, log.logger())
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	line := log.String()
	for _, want := range []string{"repointed jetstream consumer", "group=adopting", "orders.Shipped"} {
		if !strings.Contains(line, want) {
			t.Errorf("log %q does not carry %q", line, want)
		}
	}
}

// A group's settings shape its durable alone; other groups keep the defaults.
func TestAGroupsSettingsApplyToItsDurableAlone(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	log := &logBuffer{}
	tr := jsTransport(t, conn, log.logger(),
		craftnats.WithAckWait(25*time.Second),
		craftnats.WithGroupConfig("tuned",
			craftnats.AckWait(3*time.Second),
			craftnats.MaxInFlight(8),
			craftnats.DeliverPolicy(jetstream.DeliverNewPolicy),
			craftnats.ConsumerConfig(func(cfg *jetstream.ConsumerConfig) {
				cfg.Description = "set by the application"
				// craftgo's own fields are re-asserted after this runs.
				cfg.AckPolicy = jetstream.AckNonePolicy
				cfg.FilterSubject = "orders.Everything"
			})))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, []events.Subscription{
		{Event: "orders.Placed", Consumer: "A", Group: "tuned", Handle: recording(&deliveries{})},
		{Event: "orders.Shipped", Consumer: "B", Group: "plain", Handle: recording(&deliveries{})},
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	tuned := consumerConfig(t, conn, "tuned")
	if tuned.AckWait != 3*time.Second {
		t.Errorf("ack wait = %s, want the group's own", tuned.AckWait)
	}
	if tuned.DeliverPolicy != jetstream.DeliverNewPolicy {
		t.Errorf("deliver policy = %v, want the group's own", tuned.DeliverPolicy)
	}
	if tuned.Description != "set by the application" {
		t.Errorf("description = %q, want the group's own", tuned.Description)
	}
	if tuned.AckPolicy != jetstream.AckExplicitPolicy {
		t.Errorf("ack policy = %v, want AckExplicit re-asserted over the group's", tuned.AckPolicy)
	}
	if got := filtersOf(tuned); len(got) != 1 || got[0] != "orders.Placed" {
		t.Errorf("filter = %v, want the plan re-asserted over the group's", got)
	}

	plain := consumerConfig(t, conn, "plain")
	if plain.AckWait != 25*time.Second {
		t.Errorf("ack wait = %s, want the transport-wide default", plain.AckWait)
	}
	if plain.DeliverPolicy != jetstream.DeliverAllPolicy {
		t.Errorf("deliver policy = %v, want the server's default", plain.DeliverPolicy)
	}

	line := log.String()
	if !strings.Contains(line, "max_in_flight=8") || !strings.Contains(line, "max_in_flight=1") {
		t.Errorf("log %q does not show the two prefetches", line)
	}
}

// A negative group MaxInFlight fails construction.
func TestANegativeGroupMaxInFlightIsRefused(t *testing.T) {
	_, err := craftnats.NewJetStream(runJetStreamServer(t),
		craftnats.WithGroupConfig("tuned", craftnats.MaxInFlight(-1)))
	if err == nil {
		t.Fatal("a negative prefetch must be refused")
	}
	if !strings.Contains(err.Error(), "tuned") {
		t.Errorf("refusal does not name the group: %v", err)
	}
}
