package events_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
)

// optionTransport is an OptionAware transport named "probe" that reads the known options.
type optionTransport struct {
	recordingTransport
	known []string
}

func (o *optionTransport) AdapterName() string    { return "probe" }
func (o *optionTransport) KnownOptions() []string { return o.known }

func TestAnOptionSetsTheMessageKey(t *testing.T) {
	tr := &recordingTransport{}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	if err := bus.Publish(context.Background(), "orders.OrderPlaced", payload{ID: "o-1"},
		events.WithKey("o-1")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if got := tr.sent[0].Key; got != "o-1" {
		t.Errorf("key = %q, want o-1", got)
	}
}

// A publish without WithKey is keyless.
func TestAPublishWithoutTheOptionIsKeyless(t *testing.T) {
	tr := &recordingTransport{}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	if err := bus.Publish(context.Background(), "orders.OrderPlaced", payload{ID: "o-1"}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if got := tr.sent[0].Key; got != "" {
		t.Errorf("key = %q, want empty", got)
	}
}

func TestOptionsCarryDedupIDAndHeaders(t *testing.T) {
	tr := &recordingTransport{}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	if err := bus.Publish(context.Background(), "orders.OrderPlaced", payload{ID: "o-1"},
		events.WithDedupID("d-1"),
		events.WithHeader("tenant", "acme"),
		events.WithHeader("trace-parent", "tp-1")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	msg := tr.sent[0]
	if msg.DedupID != "d-1" {
		t.Errorf("dedup id = %q", msg.DedupID)
	}
	if msg.Metadata["tenant"] != "acme" || msg.Metadata["trace-parent"] != "tp-1" {
		t.Errorf("headers = %v", msg.Metadata)
	}
}

// WithHeader under a reserved key is dropped, as on an Envelope.
func TestAReservedHeaderOptionIsStillDropped(t *testing.T) {
	tr := &recordingTransport{}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	if err := bus.Publish(context.Background(), "orders.OrderPlaced", payload{ID: "o-1"},
		events.WithHeader(events.MetaCodec, "forged"),
		events.WithHeader("craftgo-key", "forged")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	msg := tr.sent[0]
	if msg.Metadata[events.MetaCodec] != "json" {
		t.Errorf("codec stamp was forged: %v", msg.Metadata)
	}
	if _, carried := msg.Metadata["craftgo-key"]; carried {
		t.Errorf("an adapter's own header was carried: %v", msg.Metadata)
	}
}

// Options apply in order, so the last one setting a value wins.
func TestTheLastOptionSettingAValueWins(t *testing.T) {
	env := events.Envelope{Event: "orders.OrderPlaced"}
	env.Apply(events.JoinOptions(
		[]events.PublishOption{events.WithKey("default"), events.WithHeader("tenant", "acme")},
		[]events.PublishOption{events.WithKey("per-call")},
	)...)
	if env.Key != "per-call" {
		t.Errorf("key = %q, want the per-call option to win", env.Key)
	}
	if env.Metadata["tenant"] != "acme" {
		t.Errorf("a default with no per-call counterpart was lost: %v", env.Metadata)
	}
}

// JoinOptions does not write into the defaults slice, even when it has spare capacity.
func TestJoinOptionsLeavesTheDefaultsAlone(t *testing.T) {
	defaults := make([]events.PublishOption, 1, 4)
	defaults[0] = events.WithKey("default")

	first := events.JoinOptions(defaults, []events.PublishOption{events.WithKey("a")})
	second := events.JoinOptions(defaults, []events.PublishOption{events.WithKey("b")})

	env := events.Envelope{}
	env.Apply(first...)
	if env.Key != "a" {
		t.Errorf("the second call overwrote the first: key = %q", env.Key)
	}
	env = events.Envelope{}
	env.Apply(second...)
	if env.Key != "b" {
		t.Errorf("key = %q, want b", env.Key)
	}
}

func TestAnAdapterReadsItsOwnOption(t *testing.T) {
	tr := &optionTransport{known: []string{"priority"}}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	if err := bus.Publish(context.Background(), "orders.OrderPlaced", payload{ID: "o-1"},
		events.WithAdapterOption("probe", "priority", 9)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	got, ok := tr.sent[0].AdapterOption("probe", "priority")
	if !ok || got != 9 {
		t.Errorf("adapter option = %v (%v), want 9", got, ok)
	}
}

// An option the configured adapter does not read fails the publish with an
// *UnknownOptionError.
func TestAnUnknownOptionInTheAdaptersOwnNamespaceErrors(t *testing.T) {
	tr := &optionTransport{known: []string{"priority"}}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	err := bus.Publish(context.Background(), "orders.OrderPlaced", payload{ID: "o-1"},
		events.WithAdapterOption("probe", "partition", 3))
	var unknown *events.UnknownOptionError
	if !errors.As(err, &unknown) {
		t.Fatalf("err = %v, want *UnknownOptionError", err)
	}
	if unknown.Adapter != "probe" || unknown.Key != "partition" || unknown.Event != "orders.OrderPlaced" {
		t.Errorf("error does not name the publish: %+v", unknown)
	}
	if !strings.Contains(unknown.Error(), "priority") {
		t.Errorf("error does not say what the adapter does read: %s", unknown)
	}
	if len(tr.sent) != 0 {
		t.Errorf("the message went out anyway: %v", tr.sent)
	}
}

// An adapter that reads no options refuses any option under its name.
func TestAnAdapterWithNoOptionsRefusesAnyOfItsOwn(t *testing.T) {
	tr := &optionTransport{}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	err := bus.Publish(context.Background(), "orders.OrderPlaced", payload{ID: "o-1"},
		events.WithAdapterOption("probe", "anything", 1))
	if err == nil {
		t.Fatal("an option under an adapter that reads none must fail the publish")
	}
	if !strings.Contains(err.Error(), "reads none") {
		t.Errorf("error = %v", err)
	}
}

// An option addressed to another adapter is ignored.
func TestAnotherAdaptersOptionIsIgnored(t *testing.T) {
	tr := &optionTransport{known: []string{"priority"}}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	if err := bus.Publish(context.Background(), "orders.OrderPlaced", payload{ID: "o-1"},
		events.WithAdapterOption("sqs", "messageGroupId", "g-1")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if _, read := tr.sent[0].AdapterOption("probe", "messageGroupId"); read {
		t.Error("another adapter's option leaked into this one's namespace")
	}
}

// A transport that is not OptionAware publishes any adapter option unchecked.
func TestATransportThatNamesNoAdapterIsNotChecked(t *testing.T) {
	tr := &recordingTransport{}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	if err := bus.Publish(context.Background(), "orders.OrderPlaced", payload{ID: "o-1"},
		events.WithAdapterOption("anything", "at-all", 1)); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

// A bad adapter option fails the batch before anything is sent.
func TestABadOptionFailsTheBatchBeforeAnythingIsSent(t *testing.T) {
	tr := &optionTransport{known: []string{"priority"}}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	envs := []events.Envelope{
		{Event: "orders.OrderPlaced", Payload: payload{ID: "o-1"}},
		{Event: "orders.OrderPlaced", Payload: payload{ID: "o-2"},
			AdapterOptions: map[string]map[string]any{"probe": {"partition": 3}}},
	}
	if err := bus.PublishAll(context.Background(), envs); err == nil {
		t.Fatal("a bad option must fail the batch")
	}
	if len(tr.sent) != 0 {
		t.Errorf("%d messages went out before the batch failed", len(tr.sent))
	}
}

// A publish default applies to Publish and PublishAll alike.
func TestAPublishDefaultRidesEveryPublish(t *testing.T) {
	tr := &recordingTransport{}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}),
		events.WithPublishDefaults(events.WithHeader("tenant", "acme")))

	if err := bus.Publish(context.Background(), "orders.OrderPlaced", payload{ID: "o-1"}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := bus.PublishAll(context.Background(), []events.Envelope{
		{Event: "orders.OrderPlaced", Payload: payload{ID: "o-2"}},
	}); err != nil {
		t.Fatalf("publish all: %v", err)
	}
	for i, msg := range tr.sent {
		if msg.Metadata["tenant"] != "acme" {
			t.Errorf("message %d carries %v, want the default header", i, msg.Metadata)
		}
	}
}

// The caller's own value beats a publish default, on both paths.
func TestAPerCallValueBeatsTheDefault(t *testing.T) {
	tr := &recordingTransport{}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}),
		events.WithPublishDefaults(events.WithKey("default"), events.WithHeader("tenant", "acme")))

	if err := bus.Publish(context.Background(), "orders.OrderPlaced", payload{ID: "o-1"},
		events.WithKey("per-call")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := bus.PublishAll(context.Background(), []events.Envelope{{
		Event:    "orders.OrderPlaced",
		Key:      "per-envelope",
		Payload:  payload{ID: "o-2"},
		Metadata: map[string]string{"tenant": "other"},
	}}); err != nil {
		t.Fatalf("publish all: %v", err)
	}

	if got := tr.sent[0].Key; got != "per-call" {
		t.Errorf("key = %q, want the per-call option to win", got)
	}
	if got := tr.sent[0].Metadata["tenant"]; got != "acme" {
		t.Errorf("a default with no per-call counterpart was lost: %q", got)
	}
	if got := tr.sent[1].Key; got != "per-envelope" {
		t.Errorf("key = %q, want the envelope's own", got)
	}
	if got := tr.sent[1].Metadata["tenant"]; got != "other" {
		t.Errorf("tenant = %q, want the envelope's own", got)
	}
}

// Applying the publish defaults leaves the caller's envelope metadata untouched.
func TestThePublishDefaultsDoNotTouchTheCallersEnvelope(t *testing.T) {
	tr := &recordingTransport{}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}),
		events.WithPublishDefaults(events.WithHeader("tenant", "acme")))

	meta := map[string]string{"hops": "1"}
	envs := []events.Envelope{{Event: "orders.OrderPlaced", Payload: payload{ID: "o-1"}, Metadata: meta}}
	if err := bus.PublishAll(context.Background(), envs); err != nil {
		t.Fatalf("publish all: %v", err)
	}
	if _, leaked := meta["tenant"]; leaked {
		t.Errorf("the default was written into the caller's metadata: %v", meta)
	}
	if got := tr.sent[0].Metadata; got["tenant"] != "acme" || got["hops"] != "1" {
		t.Errorf("message metadata = %v, want both", got)
	}
}
