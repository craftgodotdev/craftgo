package events_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
)

type payload struct {
	ID    string `json:"id"`
	Count int    `json:"count"`
}

// recordingTransport records what it is handed and delivers nothing.
type recordingTransport struct {
	mu      sync.Mutex
	sent    []*events.Message
	subs    []events.Subscription
	batches int
	err     error
}

func (r *recordingTransport) Publish(_ context.Context, msg *events.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, msg)
	return nil
}

func (r *recordingTransport) Subscribe(_ context.Context, subs []events.Subscription) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batches++
	r.subs = append(r.subs, subs...)
	return r.err
}

// start registers subs on bus and starts it.
func start(t *testing.T, ctx context.Context, bus *events.Bus, subs ...events.Subscription) {
	t.Helper()
	for _, sub := range subs {
		if err := bus.Register(sub); err != nil {
			t.Fatalf("register %s/%s: %v", sub.Event, sub.Consumer, err)
		}
	}
	if err := bus.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
}

// busOver builds a bus over a fresh recording transport.
func busOver(opts ...events.Option) (*events.Bus, *recordingTransport) {
	tr := &recordingTransport{}
	opts = append([]events.Option{events.WithTransport(tr), events.WithCodec(codecjson.Codec{})}, opts...)
	return events.New(opts...), tr
}

// order is a payload type shaped like a generated one, with its own Validate.
type order struct {
	ID    string `json:"id"`
	Count int    `json:"count"`
}

func (o *order) Validate() error {
	if o.ID == "" {
		return errors.New("id is required")
	}
	return nil
}

// tagMW returns a middleware that appends ">tag" to trace before delegating and "<tag"
// after.
func tagMW(trace *string, tag string) events.Middleware {
	return func(_ events.Subscription, next events.Handler) events.Handler {
		return func(ctx context.Context, msg *events.Message) error {
			*trace += ">" + tag
			err := next(ctx, msg)
			*trace += "<" + tag
			return err
		}
	}
}

// tracingSub builds one subscription whose handler marks the trace.
func tracingSub(trace *string, event, consumer string, group events.Group) events.Subscription {
	return events.Subscription{
		Event: event, Consumer: consumer, Group: group,
		Handle: func(context.Context, *events.Message) error {
			*trace += "|H|"
			return nil
		},
	}
}
