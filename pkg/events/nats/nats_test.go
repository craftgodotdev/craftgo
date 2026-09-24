package nats

import (
	"context"
	"errors"
	"testing"

	"github.com/nats-io/nats.go"

	events "github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
)

func TestDefaultSubjectIsTheContract(t *testing.T) {
	if got := New(nil).subject("orders.OrderPlaced"); got != "orders.OrderPlaced" {
		t.Errorf("default subject = %q", got)
	}
}

func TestSubjectMappingIsConfigurable(t *testing.T) {
	tr := New(nil, WithSubject(func(c string) string { return "evt." + c }))
	if got := tr.subject("orders.OrderPlaced"); got != "evt.orders.OrderPlaced" {
		t.Errorf("mapped subject = %q", got)
	}
}

// The ordering key and the metadata travel as headers.
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

// decode takes the contract from the subscription, not the subject.
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

// A metadata entry named like the key header does not become the key.
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

// The dedup ID travels in Nats-Msg-Id and survives a round trip.
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

func TestNoDeduplicationIDMeansNoHeader(t *testing.T) {
	out := New(nil).encode(&events.Message{Event: "orders.OrderPlaced", Payload: []byte("body")})
	if got := out.Header.Get(HeaderDedupID); got != "" {
		t.Errorf("%s = %q, want none", HeaderDedupID, got)
	}
}

// A metadata entry named like the dedup header does not become the dedup ID.
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

// A bus over either NATS transport refuses every option under the adapter's name.
func TestABusRefusesEveryNATSOption(t *testing.T) {
	for name, pub := range map[string]events.Publisher{"core": New(nil), "JetStream": &JetStream{}} {
		bus := events.New(events.WithPublisher(pub), events.WithCodec(codecjson.Codec{}))
		err := bus.Publish(context.Background(), "orders.Placed", struct{}{}, events.WithAdapterOption(Adapter, "subject", "x"))
		var unknown *events.UnknownOptionError
		if !errors.As(err, &unknown) || unknown.Adapter != Adapter || len(unknown.Known) != 0 {
			t.Errorf("%s: err = %v, want an *UnknownOptionError naming %q, which reads no option", name, err, Adapter)
		}
	}
}
