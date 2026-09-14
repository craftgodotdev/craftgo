package events_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	events "github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
)

// batchRecordingTransport takes a whole SubscribeAll slice in one call.
type batchRecordingTransport struct {
	batches [][]events.Subscription
	single  int
	err     error
}

func (t *batchRecordingTransport) Publish(context.Context, *events.Message) error { return nil }

func (t *batchRecordingTransport) Subscribe(context.Context, events.Subscription) error {
	t.single++
	return nil
}

func (t *batchRecordingTransport) SubscribeBatch(_ context.Context, subs []events.Subscription) error {
	t.batches = append(t.batches, subs)
	return t.err
}

func TestSubscribeAllHandsABatchSubscriberTheWholeSortedSlice(t *testing.T) {
	tr := &batchRecordingTransport{}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))
	handle := func(context.Context, *events.Message) error { return nil }
	err := bus.SubscribeAll(context.Background(), []events.Subscription{
		{Event: "b.Two", Consumer: "Z", Group: "g2", Handle: handle},
		{Event: "a.Two", Consumer: "B", Group: "g1", Handle: handle},
		{Event: "a.One", Consumer: "A", Group: "g1", Handle: handle},
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if tr.single != 0 {
		t.Fatalf("Subscribe was called %d time(s); a batch subscriber gets one SubscribeBatch", tr.single)
	}
	if len(tr.batches) != 1 || len(tr.batches[0]) != 3 {
		t.Fatalf("batches = %v, want one of three", tr.batches)
	}
	var got []string
	for _, sub := range tr.batches[0] {
		got = append(got, sub.GroupName()+"/"+sub.Event)
	}
	if want := "g1/a.One,g1/a.Two,g2/b.Two"; strings.Join(got, ",") != want {
		t.Errorf("order = %v, want %s", got, want)
	}
}

func TestABatchSubscriberReceivesWrappedHandlers(t *testing.T) {
	tr := &batchRecordingTransport{}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))
	if err := bus.SubscribeAll(context.Background(), []events.Subscription{{
		Event: "a.One", Consumer: "A", Group: "g",
		Handle: func(context.Context, *events.Message) error { panic("boom") },
	}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	err := tr.batches[0][0].Handle(context.Background(), &events.Message{Event: "a.One"})
	var panicked *events.PanicError
	if !errors.As(err, &panicked) {
		t.Fatalf("the handler handed to the transport is not wrapped in a recover: %v", err)
	}
}

func TestSubscribeAllChecksEveryEntryBeforeTheBatchCall(t *testing.T) {
	tr := &batchRecordingTransport{}
	bus := events.New(events.WithTransport(tr), events.WithCodecFor("a.One", codecjson.Codec{}))
	err := bus.SubscribeAll(context.Background(), []events.Subscription{
		{Event: "a.One", Consumer: "A", Group: "g"},
		{Event: "b.NoCodec", Consumer: "B", Group: "g"},
	})
	if !errors.Is(err, events.ErrNoCodec) {
		t.Fatalf("err = %v, want ErrNoCodec", err)
	}
	if len(tr.batches) != 0 {
		t.Fatal("the batch was handed over although one entry failed its checks")
	}
}

func TestABatchSubscriberFailureIsReported(t *testing.T) {
	tr := &batchRecordingTransport{err: errors.New("group already registered")}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))
	err := bus.SubscribeAll(context.Background(), []events.Subscription{
		{Event: "a.One", Consumer: "A", Group: "g"},
	})
	if err == nil || !strings.Contains(err.Error(), "group already registered") {
		t.Fatalf("err = %v, want the transport's refusal", err)
	}
}

func TestAnEmptyBatchIsNotHandedOver(t *testing.T) {
	tr := &batchRecordingTransport{}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))
	if err := bus.SubscribeAll(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if len(tr.batches) != 0 {
		t.Fatal("an empty slice reached the transport")
	}
}

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
