package kafka

import (
	"reflect"
	"strings"
	"testing"
	"time"

	kgo "github.com/segmentio/kafka-go"

	events "github.com/craftgodotdev/craftgo/pkg/events"
)

// The ordering key becomes the Kafka key, which is what puts one entity
// in one partition, and the contract always travels in a header so a
// topic carrying several contracts stays self-describing.
func TestEncodeCarriesContractAndKey(t *testing.T) {
	tr := New([]string{"localhost:9092"})
	rec := mustEncode(t, tr, &events.Message{
		Event:    "orders.OrderPlaced",
		Key:      "order-1",
		Payload:  []byte(`{"id":1}`),
		Metadata: map[string]string{"content-codec": "json"},
	})
	if string(rec.Key) != "order-1" {
		t.Errorf("kafka key = %q, want the ordering key", rec.Key)
	}
	got := map[string]string{}
	for _, h := range rec.Headers {
		got[h.Key] = string(h.Value)
	}
	if got[HeaderEvent] != "orders.OrderPlaced" {
		t.Errorf("contract header = %q", got[HeaderEvent])
	}
	if got[HeaderKey] != "order-1" {
		t.Errorf("key header = %q", got[HeaderKey])
	}
	if got["content-codec"] != "json" {
		t.Errorf("metadata did not survive: %v", got)
	}
}

// A round trip must preserve everything a consumer reads.
func TestDecodeRoundTrip(t *testing.T) {
	tr := New(nil)
	in := &events.Message{
		Event:    "orders.OrderPlaced",
		Key:      "order-1",
		Payload:  []byte("body"),
		Metadata: map[string]string{"content-codec": "json"},
	}
	out := decode("orders.OrderPlaced", mustEncode(t, tr, in))
	if out.Event != in.Event || out.Key != in.Key || string(out.Payload) != string(in.Payload) {
		t.Errorf("round trip lost data: %+v", out)
	}
	if out.Metadata["content-codec"] != "json" {
		t.Errorf("metadata lost: %v", out.Metadata)
	}
	// The craftgo headers are consumed, not leaked back as metadata.
	if _, leaked := out.Metadata[HeaderEvent]; leaked {
		t.Error("the contract header leaked into metadata")
	}
}

// A custom mapping reaches both the writer and the subscription. Mapping
// two contracts onto one topic is not supported for consuming - Subscribe
// refuses the second reader - but the mapping itself is one function and
// both halves must read it.
func TestTopicMappingCollapsesContracts(t *testing.T) {
	tr := New(nil, WithTopic(func(string) string { return "orders" }))
	if got := tr.topic("orders.OrderPlaced"); got != "orders" {
		t.Errorf("topic = %q, want the collapsed name", got)
	}
	if got := tr.topic("orders.OrderShipped"); got != "orders" {
		t.Errorf("second contract mapped to %q", got)
	}
}

// A default transport maps one contract to one topic.
func TestDefaultTopicIsTheContract(t *testing.T) {
	if got := New(nil).topic("orders.OrderPlaced"); got != "orders.OrderPlaced" {
		t.Errorf("default topic = %q", got)
	}
}

// THE LOSSY SHAPE: one group, one topic, two DIFFERENT contracts. The
// readers would be two members splitting the topic's partitions, each
// commit-and-skipping the other's contract. Only a WithTopic mapping
// that collapses contracts can reach it.
func TestOneGroupCannotReadTwoContractsOnOneTopic(t *testing.T) {
	tr := New(nil, WithTopic(func(string) string { return "orders" }))
	if err := tr.claim("worker", tr.topic("orders.Placed"), "orders.Placed"); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	err := tr.claim("worker", tr.topic("orders.Cancelled"), "orders.Cancelled")
	if err == nil {
		t.Fatal("one group reading two contracts on one topic must be refused")
	}
	for _, want := range []string{
		`consumer group "worker" already reads topic "orders" for contract "orders.Placed"`,
		"splitting the topic's partitions",
		"both would lose messages",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err %v does not explain %q", err, want)
		}
	}
}

// THE BENIGN SHAPE: one group, one topic, the SAME contract twice -
// ordinary replicas dividing the partitions, which is what the
// in-process transport does with two identical subscriptions.
func TestOneGroupMayRunReplicasOnOneContract(t *testing.T) {
	tr := New(nil)
	topic := tr.topic("orders.Placed")
	for i := 0; i < 3; i++ {
		if err := tr.claim("worker", topic, "orders.Placed"); err != nil {
			t.Fatalf("replica %d refused: %v", i, err)
		}
	}
}

// Different topics under one group are the feature and stay allowed, and
// so is one topic read by two groups.
func TestOneGroupReadsSeveralTopics(t *testing.T) {
	tr := New(nil)
	for _, contract := range []string{"orders.Placed", "orders.Cancelled"} {
		if err := tr.claim("worker", tr.topic(contract), contract); err != nil {
			t.Fatalf("claim %s: %v", contract, err)
		}
	}
	if err := tr.claim("auditor", tr.topic("orders.Placed"), "orders.Placed"); err != nil {
		t.Fatalf("a second group on one topic must be allowed: %v", err)
	}
}

// A claim is held by every live reader, so one replica ending must not
// free it while its siblings are still reading.
func TestGroupClaimIsReleasedByTheLastReader(t *testing.T) {
	tr := New(nil)
	for i := 0; i < 2; i++ {
		if err := tr.claim("worker", "orders", "orders.Placed"); err != nil {
			t.Fatalf("claim %d: %v", i, err)
		}
	}
	tr.release("worker", "orders")
	if err := tr.claim("worker", "orders", "orders.Cancelled"); err == nil {
		t.Error("the surviving replica still holds the claim")
	}
	tr.release("worker", "orders")
	if err := tr.claim("worker", "orders", "orders.Cancelled"); err != nil {
		t.Errorf("claim after the last reader left: %v", err)
	}
}

// Closing drops every claim, so a transport reused after Close starts
// clean.
func TestCloseReleasesGroupClaims(t *testing.T) {
	tr := New(nil)
	if err := tr.claim("worker", "orders", "orders.Placed"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := tr.claim("worker", "orders", "orders.Cancelled"); err != nil {
		t.Errorf("claim after close: %v", err)
	}
}

// A caller's metadata rides a record header and comes back. There is no
// broker harness here, so the proof is at the encode/decode boundary the
// adapter owns.
func TestCallerMetadataRoundTripsThroughARecord(t *testing.T) {
	tr := New(nil)
	in := &events.Message{
		Event:   "orders.OrderPlaced",
		Key:     "order-1",
		Payload: []byte("body"),
		Metadata: map[string]string{
			"content-codec": "json",
			"hops":          "2",
			"dead-letter":   "true",
		},
	}
	out := decode("orders.OrderPlaced", mustEncode(t, tr, in))
	if !reflect.DeepEqual(out.Metadata, in.Metadata) {
		t.Fatalf("metadata = %v, want %v", out.Metadata, in.Metadata)
	}
}

// The contract and the key headers are this adapter's. A metadata entry
// under one of their names must not be written a second time: decode
// reads the last header of a name, so a duplicate would rename the
// message or move it to another entity.
func TestReservedHeadersAreNotForgedByMetadata(t *testing.T) {
	tr := New(nil)
	rec := mustEncode(t, tr, &events.Message{
		Event:   "orders.OrderPlaced",
		Key:     "order-1",
		Payload: []byte("body"),
		Metadata: map[string]string{
			HeaderEvent: "orders.SomethingElse",
			HeaderKey:   "order-2",
		},
	})
	for _, name := range []string{HeaderEvent, HeaderKey} {
		seen := 0
		for _, h := range rec.Headers {
			if h.Key == name {
				seen++
			}
		}
		if seen != 1 {
			t.Errorf("%s written %d times, want once", name, seen)
		}
	}
	out := decode("orders.OrderPlaced", rec)
	if out.Event != "orders.OrderPlaced" || out.Key != "order-1" {
		t.Fatalf("metadata forged the contract or the key: %+v", out)
	}
}

// A message with no ordering key must reach the balancer with a nil Key.
// kgo.Hash round-robins only on nil; []byte("") is non-nil and hashes to
// one partition, so every keyless message would pin to it.
func TestAKeylessMessageCarriesANilKey(t *testing.T) {
	tr := New([]string{"localhost:9092"})
	rec := mustEncode(t, tr, &events.Message{Event: "shop.Placed", Payload: []byte(`{}`)})
	if rec.Key != nil {
		t.Errorf("Key = %#v, want nil - a non-nil empty key pins every keyless message to one partition", rec.Key)
	}
	keyed := mustEncode(t, tr, &events.Message{Event: "shop.Placed", Key: "o-1", Payload: []byte(`{}`)})
	if string(keyed.Key) != "o-1" {
		t.Errorf("Key = %q, want %q", keyed.Key, "o-1")
	}
}

// mustEncode builds the Kafka record for msg, failing the test if this
// adapter refuses it.
func mustEncode(t *testing.T, tr *Transport, msg *events.Message) kgo.Message {
	t.Helper()
	rec, err := tr.encode(msg)
	if err != nil {
		t.Fatalf("encode %s: %v", msg.Event, err)
	}
	return rec
}

// The timestamp option is this adapter's own, and reaches the record.
func TestTheTimestampOptionSetsTheRecordTime(t *testing.T) {
	tr := New(nil)
	want := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	rec := mustEncode(t, tr, &events.Message{
		Event:          "shop.Placed",
		Payload:        []byte(`{}`),
		AdapterOptions: map[string]map[string]any{Adapter: {OptionTimestamp: want}},
	})
	if !rec.Time.Equal(want) {
		t.Errorf("record time = %v, want %v", rec.Time, want)
	}
}

// The option key is this adapter's, so a value of the wrong type is a
// mistake it must report rather than ignore.
func TestATimestampOfTheWrongTypeFailsThePublish(t *testing.T) {
	tr := New(nil)
	_, err := tr.encode(&events.Message{
		Event:          "shop.Placed",
		Payload:        []byte(`{}`),
		AdapterOptions: map[string]map[string]any{Adapter: {OptionTimestamp: "yesterday"}},
	})
	if err == nil {
		t.Fatal("a timestamp option that is not a time.Time must fail the publish")
	}
	if !strings.Contains(err.Error(), OptionTimestamp) {
		t.Errorf("error does not name the option: %v", err)
	}
}

// An option addressed to another adapter is not this one's business.
func TestAnotherAdaptersOptionIsIgnored(t *testing.T) {
	tr := New(nil)
	rec := mustEncode(t, tr, &events.Message{
		Event:          "shop.Placed",
		Payload:        []byte(`{}`),
		AdapterOptions: map[string]map[string]any{"sqs": {"messageGroupId": "g-1"}},
	})
	if !rec.Time.IsZero() {
		t.Errorf("another adapter's option changed this record: %+v", rec)
	}
}
