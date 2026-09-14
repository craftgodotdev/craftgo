package nats

import (
	"testing"

	"github.com/nats-io/nats.go"

	events "github.com/craftgodotdev/craftgo/pkg/events"
)

// A contract is already subject-shaped, so the default mapping is the
// contract unchanged and a wildcard subscription keeps working.
func TestDefaultSubjectIsTheContract(t *testing.T) {
	if got := New(nil).subject("orders.OrderPlaced"); got != "orders.OrderPlaced" {
		t.Errorf("default subject = %q", got)
	}
}

// A broker whose naming is not yours to choose is what WithSubject is for.
func TestSubjectMappingIsConfigurable(t *testing.T) {
	tr := New(nil, WithSubject(func(c string) string { return "evt." + c }))
	if got := tr.subject("orders.OrderPlaced"); got != "evt.orders.OrderPlaced" {
		t.Errorf("mapped subject = %q", got)
	}
}

// The ordering key and the codec stamp ride headers, so a consumer reads
// both without decoding the payload.
func TestEncodeCarriesKeyAndMetadata(t *testing.T) {
	tr := New(nil)
	out := tr.encode(&events.Message{
		Event:    "orders.OrderPlaced",
		Key:      "order-1",
		Payload:  []byte("body"),
		Metadata: map[string]string{"content-codec": "json"},
	})
	if out.Subject != "orders.OrderPlaced" {
		t.Errorf("subject = %q", out.Subject)
	}
	if out.Header.Get(HeaderKey) != "order-1" {
		t.Errorf("key header = %q", out.Header.Get(HeaderKey))
	}
	if out.Header.Get("content-codec") != "json" {
		t.Errorf("metadata lost: %v", out.Header)
	}
}

// The contract comes from the subscription, so a custom subject mapping
// does not have to be reversible.
func TestDecodeUsesTheSubscriptionContract(t *testing.T) {
	m := &nats.Msg{Subject: "evt.orders.OrderPlaced", Data: []byte("body"), Header: nats.Header{}}
	m.Header.Set(HeaderKey, "order-1")
	m.Header.Set("content-codec", "json")

	out := decode("orders.OrderPlaced", m)
	if out.Event != "orders.OrderPlaced" {
		t.Errorf("contract = %q, want the subscription's", out.Event)
	}
	if out.Key != "order-1" {
		t.Errorf("key = %q", out.Key)
	}
	if out.Metadata["content-codec"] != "json" {
		t.Errorf("metadata lost: %v", out.Metadata)
	}
}

// The key header is this adapter's. A metadata entry under that name is
// skipped, so a message published without a key does not arrive carrying
// the caller's value as one.
func TestTheKeyHeaderIsNotForgedByMetadata(t *testing.T) {
	out := New(nil).encode(&events.Message{
		Event:    "orders.OrderPlaced",
		Payload:  []byte("body"),
		Metadata: map[string]string{HeaderKey: "order-2"},
	})
	if got := out.Header.Get(HeaderKey); got != "" {
		t.Errorf("key header = %q, want none - the message had no key", got)
	}
	if got := decode("orders.OrderPlaced", out).Key; got != "" {
		t.Errorf("decoded key = %q, want none", got)
	}
}

// The key is consumed into Key, so a consumer sees the same metadata
// entries here as on any other transport.
func TestTheKeyHeaderIsNotLeftInMetadata(t *testing.T) {
	m := &nats.Msg{Subject: "orders.OrderPlaced", Data: []byte("body"), Header: nats.Header{}}
	m.Header.Set(HeaderKey, "order-1")
	m.Header.Set("hops", "2")

	out := decode("orders.OrderPlaced", m)
	if out.Key != "order-1" {
		t.Errorf("key = %q", out.Key)
	}
	if _, leaked := out.Metadata[HeaderKey]; leaked {
		t.Errorf("the key header leaked into metadata: %v", out.Metadata)
	}
	if out.Metadata["hops"] != "2" {
		t.Errorf("caller metadata lost: %v", out.Metadata)
	}
}

// The deduplication ID rides Nats-Msg-Id, which is the name a JetStream
// stream with a duplicate window reads. Core NATS carries it to the
// consumer without acting on it, and carrying it is what lets a consumer
// recognise a repeat for itself.
func TestEncodeCarriesTheDeduplicationID(t *testing.T) {
	out := New(nil).encode(&events.Message{
		Event:   "orders.OrderPlaced",
		Key:     "order-1",
		DedupID: "attempt-7",
		Payload: []byte("body"),
	})
	if got := out.Header.Get(HeaderDedupID); got != "attempt-7" {
		t.Errorf("%s = %q, want attempt-7", HeaderDedupID, got)
	}
	if got := decode("orders.OrderPlaced", out).DedupID; got != "attempt-7" {
		t.Errorf("round trip lost the dedup id: %q", got)
	}
}

// A message published without one carries no header, so a consumer can
// tell "no identity given" from "this identity".
func TestNoDeduplicationIDMeansNoHeader(t *testing.T) {
	out := New(nil).encode(&events.Message{Event: "orders.OrderPlaced", Payload: []byte("body")})
	if got := out.Header.Get(HeaderDedupID); got != "" {
		t.Errorf("%s = %q, want none", HeaderDedupID, got)
	}
}

// The header is this adapter's, so a metadata entry under that name is
// skipped rather than arriving as the message's deduplication identity.
func TestTheDeduplicationHeaderIsNotForgedByMetadata(t *testing.T) {
	out := New(nil).encode(&events.Message{
		Event:    "orders.OrderPlaced",
		Payload:  []byte("body"),
		Metadata: map[string]string{HeaderDedupID: "forged"},
	})
	if got := out.Header.Get(HeaderDedupID); got != "" {
		t.Errorf("%s = %q, want none - the message had no dedup id", HeaderDedupID, got)
	}
	if got := decode("orders.OrderPlaced", out).DedupID; got != "" {
		t.Errorf("decoded dedup id = %q, want none", got)
	}
}

// It is consumed into DedupID, so a consumer sees the same metadata
// entries here as on any other transport.
func TestTheDeduplicationHeaderIsNotLeftInMetadata(t *testing.T) {
	m := &nats.Msg{Subject: "orders.OrderPlaced", Data: []byte("body"), Header: nats.Header{}}
	m.Header.Set(HeaderDedupID, "attempt-7")
	m.Header.Set("hops", "2")

	out := decode("orders.OrderPlaced", m)
	if out.DedupID != "attempt-7" {
		t.Errorf("dedup id = %q", out.DedupID)
	}
	if _, leaked := out.Metadata[HeaderDedupID]; leaked {
		t.Errorf("the dedup header leaked into metadata: %v", out.Metadata)
	}
	if out.Metadata["hops"] != "2" {
		t.Errorf("caller metadata lost: %v", out.Metadata)
	}
}
