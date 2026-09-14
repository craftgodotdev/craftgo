package events_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
)

// optionTransport is a transport that names itself to the option check
// and reads one option of its own - the shape every broker adapter has.
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

// Nothing fills a key in on the caller's behalf: a publish without the
// option is keyless, which is what lets a transport spread those messages
// instead of piling them onto one partition.
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

// A header under a reserved name stays the runtime's or the adapter's,
// the same way one set on the envelope directly does.
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

// Options apply in order, so the last one setting a value wins. That is
// the whole mechanism behind publisher defaults.
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

// JoinOptions must not write into either input: a publisher hands the
// same defaults slice to every call it makes.
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

// The rule this whole surface exists for: an option the configured
// adapter does not read must fail the publish, not vanish.
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

// An adapter that reads nothing still gets the check - an option under
// its name is then always a mistake.
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

// An option meant for another broker is that broker's business, so the
// same publishing code runs whichever transport is wired up.
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

// A transport that names no adapter cannot be checked - nothing can tell
// an option meant for it from one meant for somebody else - so it
// publishes rather than refusing.
func TestATransportThatNamesNoAdapterIsNotChecked(t *testing.T) {
	tr := &recordingTransport{}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	if err := bus.Publish(context.Background(), "orders.OrderPlaced", payload{ID: "o-1"},
		events.WithAdapterOption("anything", "at-all", 1)); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

// The check runs while the batch is encoded, which is before anything is
// sent - the same promise an unencodable payload gets.
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
