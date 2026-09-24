package kafka

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	events "github.com/craftgodotdev/craftgo/pkg/events"
)

// The ordering key becomes the record key and the contract a header.
func TestEncodeCarriesContractAndKey(t *testing.T) {
	tr := New([]string{"localhost:9092"})
	rec := mustEncode(t, tr, &events.Message{
		Event:    "orders.OrderPlaced",
		Key:      "order-1",
		Payload:  []byte(`{"id":1}`),
		Metadata: map[string]string{"content-codec": "json"},
	})
	if string(rec.Key) != "order-1" {
		t.Errorf("record key = %q, want the ordering key", rec.Key)
	}
	if rec.Topic != "orders.OrderPlaced" {
		t.Errorf("topic = %q, want the contract", rec.Topic)
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

// A round trip preserves everything a consumer reads.
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

// WithTopic can map several contracts onto one topic.
func TestTopicMappingCollapsesContracts(t *testing.T) {
	tr := New(nil, WithTopic(func(string) string { return "orders" }))
	if got := tr.topic("orders.OrderPlaced"); got != "orders" {
		t.Errorf("topic = %q, want the collapsed name", got)
	}
	if got := tr.topic("orders.OrderShipped"); got != "orders" {
		t.Errorf("second contract mapped to %q", got)
	}
}

func TestDefaultTopicIsTheContract(t *testing.T) {
	if got := New(nil).topic("orders.OrderPlaced"); got != "orders.OrderPlaced" {
		t.Errorf("default topic = %q", got)
	}
}

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
		`group "worker" already reads topic "orders" for contract "orders.Placed"`,
		"dividing the topic between them",
		"both would lose messages",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err %v does not explain %q", err, want)
		}
	}
}

func TestOneGroupMayRunReplicasOnOneContract(t *testing.T) {
	tr := New(nil)
	topic := tr.topic("orders.Placed")
	for i := 0; i < 3; i++ {
		if err := tr.claim("worker", topic, "orders.Placed"); err != nil {
			t.Fatalf("replica %d refused: %v", i, err)
		}
	}
}

// One group may read several topics, and two groups may read one topic.
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

// Metadata named like the contract or key header is not written again.
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

// A keyless message's record key is nil, so the partitioner does not hash it.
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

// mustEncode returns tr's record for msg, failing the test on an error.
func mustEncode(t *testing.T, tr *Transport, msg *events.Message) *kgo.Record {
	t.Helper()
	rec, err := tr.encode(msg)
	if err != nil {
		t.Fatalf("encode %s: %v", msg.Event, err)
	}
	return rec
}

func TestTheTimestampOptionSetsTheRecordTime(t *testing.T) {
	tr := New(nil)
	want := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	rec := mustEncode(t, tr, &events.Message{
		Event:          "shop.Placed",
		Payload:        []byte(`{}`),
		AdapterOptions: map[string]map[string]any{Adapter: {OptionTimestamp: want}},
	})
	if !rec.Timestamp.Equal(want) {
		t.Errorf("record timestamp = %v, want %v", rec.Timestamp, want)
	}
}

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

func TestAnotherAdaptersOptionIsIgnored(t *testing.T) {
	tr := New(nil)
	rec := mustEncode(t, tr, &events.Message{
		Event:          "shop.Placed",
		Payload:        []byte(`{}`),
		AdapterOptions: map[string]map[string]any{"sqs": {"messageGroupId": "g-1"}},
	})
	if !rec.Timestamp.IsZero() {
		t.Errorf("another adapter's option changed this record: %+v", rec)
	}
}

// Only a share group can redeliver and reject; every mode settles.
func TestCanDispositionFollowsTheMode(t *testing.T) {
	classic := New(nil)
	share := New(nil, WithShareGroup())

	for _, d := range []events.Disposition{events.DispositionRedeliver, events.DispositionReject} {
		if classic.CanDisposition(d) {
			t.Errorf("a classic consumer group claims it can %v", d)
		}
		if !share.CanDisposition(d) {
			t.Errorf("a share group claims it cannot %v", d)
		}
	}
	for _, tr := range []*Transport{classic, share} {
		if !tr.CanDisposition(events.DispositionSettle) {
			t.Error("every mode settles")
		}
		if tr.CanDisposition(events.DispositionUnset) {
			t.Error("unset is not something a transport honours")
		}
	}
}

// The cap turns a Redeliver into a reject but never rejects a success.
func TestMaxDeliveriesBoundsRedeliveryAndNotSuccess(t *testing.T) {
	tr := New(nil, WithShareGroup(), WithMaxDeliveries(3))

	asked := &events.Message{}
	asked.SetDeliveries(2)
	asked.Redeliver()
	if got := tr.ackFor(asked); got != kgo.AckRelease {
		t.Errorf("delivery 2 of 3 = %v, want release", got)
	}

	atCap := &events.Message{}
	atCap.SetDeliveries(3)
	atCap.Redeliver()
	if got := tr.ackFor(atCap); got != kgo.AckReject {
		t.Errorf("delivery 3 of 3 = %v, want reject - the loop has to end", got)
	}

	succeeded := &events.Message{}
	succeeded.SetDeliveries(9)
	if got := tr.ackFor(succeeded); got != kgo.AckAccept {
		t.Errorf("a delivery that succeeded on attempt 9 = %v, want accept", got)
	}

	unbounded := New(nil, WithShareGroup(), WithMaxDeliveries(0))
	forever := &events.Message{}
	forever.SetDeliveries(1000)
	forever.Redeliver()
	if got := unbounded.ackFor(forever); got != kgo.AckRelease {
		t.Errorf("an unbounded transport = %v, want release", got)
	}
}

func TestAnUnsetDispositionSettles(t *testing.T) {
	tr := New(nil, WithShareGroup())
	if got := tr.ackFor(&events.Message{}); got != kgo.AckAccept {
		t.Errorf("ackFor(unset) = %v, want accept", got)
	}
	rejected := &events.Message{}
	rejected.Reject()
	if got := tr.ackFor(rejected); got != kgo.AckReject {
		t.Errorf("ackFor(reject) = %v, want reject", got)
	}
}

// The dedup ID travels in its own header and is not left in Metadata.
func TestTheDeduplicationIDSurvivesARoundTrip(t *testing.T) {
	tr := New(nil)
	in := &events.Message{
		Event:    "orders.OrderPlaced",
		Key:      "order-1",
		DedupID:  "attempt-7",
		Payload:  []byte("body"),
		Metadata: map[string]string{"content-codec": "json"},
	}
	rec := mustEncode(t, tr, in)

	var onRecord string
	for _, h := range rec.Headers {
		if h.Key == HeaderDedupID {
			onRecord = string(h.Value)
		}
	}
	if onRecord != "attempt-7" {
		t.Errorf("the record carries dedup id %q, want attempt-7", onRecord)
	}

	out := decode("orders.OrderPlaced", rec)
	if out.DedupID != "attempt-7" {
		t.Errorf("round trip lost the dedup id: %q", out.DedupID)
	}
	// It must not come back as metadata too.
	if _, leaked := out.Metadata[HeaderDedupID]; leaked {
		t.Errorf("the dedup header leaked into metadata: %v", out.Metadata)
	}
}

func TestNoDeduplicationIDMeansNoHeader(t *testing.T) {
	rec := mustEncode(t, New(nil), &events.Message{Event: "shop.Placed", Payload: []byte(`{}`)})
	for _, h := range rec.Headers {
		if h.Key == HeaderDedupID {
			t.Errorf("a message with no dedup id carries %q", h.Value)
		}
	}
	if got := decode("shop.Placed", rec).DedupID; got != "" {
		t.Errorf("DedupID = %q, want empty", got)
	}
}

func TestTheDeduplicationHeaderCannotBeForgedByMetadata(t *testing.T) {
	rec := mustEncode(t, New(nil), &events.Message{
		Event:    "shop.Placed",
		DedupID:  "real",
		Payload:  []byte(`{}`),
		Metadata: map[string]string{HeaderDedupID: "forged"},
	})
	seen := 0
	for _, h := range rec.Headers {
		if h.Key == HeaderDedupID {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("%s written %d times, want once", HeaderDedupID, seen)
	}
	if got := decode("shop.Placed", rec).DedupID; got != "real" {
		t.Errorf("DedupID = %q, want the message's own", got)
	}
}

// The dedup header is under events.MetaPrefix, so WithHeader cannot set it.
func TestTheDeduplicationHeaderNameIsReserved(t *testing.T) {
	if !events.IsReservedMeta(HeaderDedupID) {
		t.Errorf("%s is not reserved, so WithHeader could set it", HeaderDedupID)
	}
}
