package events_test

import (
	"errors"
	"strings"
	"testing"

	events "github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
)

func TestDecodeNamesAnUndecodablePayload(t *testing.T) {
	bus := events.New(events.WithCodec(codecjson.Codec{}))
	var out struct{ ID string }
	err := bus.Decode(&events.Message{Event: "orders.Placed", Payload: []byte("{")}, &out)
	var payloadErr *events.PayloadError
	if !errors.As(err, &payloadErr) {
		t.Fatalf("err = %T %v, want *PayloadError", err, err)
	}
	if payloadErr.Event != "orders.Placed" || !strings.Contains(err.Error(), "orders.Placed") {
		t.Errorf("the error does not name the contract: %v", err)
	}
	if errors.Is(err, events.ErrCodecMismatch) {
		t.Error("a malformed payload is not a codec mismatch")
	}
}

func TestACodecMismatchIsNotAPayloadError(t *testing.T) {
	bus := events.New(events.WithCodec(codecjson.Codec{}))
	var out struct{ ID string }
	err := bus.Decode(&events.Message{
		Event:    "orders.Placed",
		Payload:  []byte(`{"ID":"1"}`),
		Metadata: map[string]string{events.MetaCodec: "protobuf"},
	}, &out)
	if !errors.Is(err, events.ErrCodecMismatch) {
		t.Fatalf("err = %v, want ErrCodecMismatch", err)
	}
	var payloadErr *events.PayloadError
	if errors.As(err, &payloadErr) {
		t.Error("a codec mismatch is a configuration error, not a poison payload")
	}
	if !strings.Contains(err.Error(), "orders.Placed") {
		t.Errorf("the error does not name the contract: %v", err)
	}
}
