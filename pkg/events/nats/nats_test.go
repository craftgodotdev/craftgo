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

// reservedHeaders are the headers that carry a message field.
var reservedHeaders = []struct {
	name  string
	field func(*events.Message) string
}{
	{HeaderKey, func(m *events.Message) string { return m.Key }},
	{HeaderDedupID, func(m *events.Message) string { return m.DedupID }},
}

// A metadata entry named like a reserved header does not become its field.
func TestReservedHeadersAreNotForgedByMetadata(t *testing.T) {
	for _, h := range reservedHeaders {
		out := New(nil).encode(&events.Message{
			Event:    "orders.OrderPlaced",
			Payload:  []byte("body"),
			Metadata: map[string]string{h.name: "forged"},
		})
		if got := out.Header.Get(h.name); got != "" {
			t.Errorf("%s = %q, want none - the message had no value for it", h.name, got)
		}
		if got := h.field(decode("orders.OrderPlaced", out)); got != "" {
			t.Errorf("%s decoded as %q, want none", h.name, got)
		}
	}
}

// A reserved header becomes its field and stays out of the metadata.
func TestReservedHeadersAreNotLeftInMetadata(t *testing.T) {
	for _, h := range reservedHeaders {
		m := &nats.Msg{Subject: "orders.OrderPlaced", Data: []byte("body"), Header: nats.Header{}}
		m.Header.Set(h.name, "v-1")
		m.Header.Set("hops", "2")

		out := decode("orders.OrderPlaced", m)
		if got := h.field(out); got != "v-1" {
			t.Errorf("%s decoded as %q, want v-1", h.name, got)
		}
		if _, leaked := out.Metadata[h.name]; leaked {
			t.Errorf("the %s header leaked into metadata: %v", h.name, out.Metadata)
		}
		if out.Metadata["hops"] != "2" {
			t.Errorf("%s: caller metadata lost: %v", h.name, out.Metadata)
		}
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
