package logging_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
	"github.com/craftgodotdev/craftgo/pkg/events/logging"
	"github.com/craftgodotdev/craftgo/pkg/events/memory"
)

// recorder captures what a handler was asked to write, so a test can
// assert on records rather than on formatted text.
type recorder struct {
	mu      sync.Mutex
	records []slog.Record
	ctxs    []context.Context
}

func (r *recorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *recorder) Handle(ctx context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, rec)
	r.ctxs = append(r.ctxs, ctx)
	return nil
}

func (r *recorder) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *recorder) WithGroup(string) slog.Handler      { return r }

func (r *recorder) lines() []slog.Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]slog.Record(nil), r.records...)
}

func (r *recorder) contexts() []context.Context {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]context.Context(nil), r.ctxs...)
}

func attrsOf(rec slog.Record) map[string]string {
	out := map[string]string{}
	rec.Attrs(func(a slog.Attr) bool {
		out[a.Key] = a.Value.String()
		return true
	})
	return out
}

// deliver runs one message through a bus carrying the access log.
func deliver(t *testing.T, h slog.Handler, handler events.Handler, opts ...logging.AccessLogOption) {
	t.Helper()
	tr := memory.New()
	bus := events.New(
		events.WithTransport(tr),
		events.WithCodec(codecjson.Codec{}),
		events.WithMiddleware(logging.AccessLog(slog.New(h), opts...)),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := bus.Subscribe(ctx, events.Subscription{
		Event: "orders.Placed", Consumer: "SendReceipt", Group: "receipts",
		Handle: handler,
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := tr.Publish(context.Background(), &events.Message{
		Event: "orders.Placed", Key: "o-1", Payload: []byte(`{}`),
		Metadata: map[string]string{events.MetaCodec: "json"},
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	tr.Drain()
}

// One delivery is ONE line. The transport's error handler already logs a
// failure at Error, so a second line here would report one failure twice.
func TestOneDeliveryIsOneLine(t *testing.T) {
	rec := &recorder{}
	deliver(t, rec, func(context.Context, *events.Message) error { return nil })

	lines := rec.lines()
	if len(lines) != 1 {
		t.Fatalf("wrote %d lines for one delivery, want 1", len(lines))
	}
	got := attrsOf(lines[0])
	for k, want := range map[string]string{
		"event": "orders.Placed", "consumer": "SendReceipt", "group": "receipts", "key": "o-1",
	} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
	if _, hasErr := got["error"]; hasErr {
		t.Errorf("a success carries an error attribute: %v", got)
	}
	if _, hasTook := got["took"]; !hasTook {
		t.Errorf("no duration on the line: %v", got)
	}
}

// A FAILED delivery is also one line, at the SAME level, carrying the
// error - not a second line at Error beside the transport's own.
func TestAFailedDeliveryIsOneLineAtTheSameLevel(t *testing.T) {
	rec := &recorder{}
	boom := errors.New("mail provider unavailable")
	deliver(t, rec, func(context.Context, *events.Message) error { return boom })

	lines := rec.lines()
	if len(lines) != 1 {
		t.Fatalf("wrote %d lines for one failed delivery, want 1", len(lines))
	}
	if lines[0].Level != slog.LevelInfo {
		t.Errorf("level = %v, want Info - the transport's error handler is what logs at Error", lines[0].Level)
	}
	if got := attrsOf(lines[0])["error"]; !strings.Contains(got, "mail provider unavailable") {
		t.Errorf("error attribute = %q", got)
	}
}

// The handler's error reaches the transport unchanged: a logging
// middleware observes, it does not decide.
func TestTheHandlersErrorIsPassedThrough(t *testing.T) {
	boom := errors.New("boom")
	var seen error
	tr := memory.New(memory.WithErrorHandler(func(_ events.Subscription, _ *events.Message, err error) {
		seen = err
	}))
	bus := events.New(
		events.WithTransport(tr),
		events.WithCodec(codecjson.Codec{}),
		events.WithMiddleware(logging.AccessLog(slog.New(&recorder{}))),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := bus.Subscribe(ctx, events.Subscription{
		Event: "orders.Placed", Consumer: "C", Group: "g",
		Handle: func(context.Context, *events.Message) error { return boom },
	}); err != nil {
		t.Fatal(err)
	}
	_ = tr.Publish(context.Background(), &events.Message{
		Event: "orders.Placed", Payload: []byte(`{}`),
		Metadata: map[string]string{events.MetaCodec: "json"},
	})
	tr.Drain()
	if !errors.Is(seen, boom) {
		t.Errorf("transport saw %v, want the handler's own error", seen)
	}
}

// THE CONTEXT MUST REACH THE HANDLER. slog's level shortcuts pass a
// background context of their own, so a line written with Info() would
// look right and silently carry no trace id. LogAttrs(ctx, ...) is what
// makes the handler's ctx the delivery's.
func TestTheDeliveryContextReachesTheHandler(t *testing.T) {
	type ctxKey struct{}
	rec := &recorder{}
	tr := memory.New()
	bus := events.New(
		events.WithTransport(tr),
		events.WithCodec(codecjson.Codec{}),
		// Outside the access log, so the value is on the context the log
		// is handed.
		events.WithMiddleware(
			func(_ events.Subscription, next events.Handler) events.Handler {
				return func(ctx context.Context, msg *events.Message) error {
					return next(context.WithValue(ctx, ctxKey{}, "trace-42"), msg)
				}
			},
			logging.AccessLog(slog.New(rec)),
		),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := bus.Subscribe(ctx, events.Subscription{
		Event: "orders.Placed", Consumer: "C", Group: "g",
		Handle: func(context.Context, *events.Message) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}
	_ = tr.Publish(context.Background(), &events.Message{
		Event: "orders.Placed", Payload: []byte(`{}`),
		Metadata: map[string]string{events.MetaCodec: "json"},
	})
	tr.Drain()

	ctxs := rec.contexts()
	if len(ctxs) != 1 {
		t.Fatalf("handler saw %d contexts, want 1", len(ctxs))
	}
	if got := ctxs[0].Value(ctxKey{}); got != "trace-42" {
		t.Errorf("the handler's context carries %v, want trace-42 - the delivery context did not reach it", got)
	}
}

// A skipped contract is not wrapped at all, so it costs nothing rather
// than costing a check per delivery.
func TestASkippedContractIsNotLogged(t *testing.T) {
	rec := &recorder{}
	deliver(t, rec, func(context.Context, *events.Message) error { return nil },
		logging.AccessLogSkipContracts("orders.Placed"))
	if n := len(rec.lines()); n != 0 {
		t.Errorf("wrote %d lines for a skipped contract, want 0", n)
	}
}

// The level is configurable, and a line below the running level is not
// built.
func TestTheLevelIsConfigurable(t *testing.T) {
	rec := &recorder{}
	deliver(t, rec, func(context.Context, *events.Message) error { return nil },
		logging.AccessLogLevel(slog.LevelDebug))
	lines := rec.lines()
	if len(lines) != 1 {
		t.Fatalf("wrote %d lines, want 1", len(lines))
	}
	if lines[0].Level != slog.LevelDebug {
		t.Errorf("level = %v, want Debug", lines[0].Level)
	}
}

// Extra fields are derived per delivery and land on the line.
func TestExtraFieldsReachTheLine(t *testing.T) {
	rec := &recorder{}
	deliver(t, rec, func(context.Context, *events.Message) error { return nil },
		logging.AccessLogFields(func(_ context.Context, msg *events.Message) []slog.Attr {
			return []slog.Attr{slog.String("tenant", msg.Metadata["tenant"])}
		}))
	lines := rec.lines()
	if len(lines) != 1 {
		t.Fatalf("wrote %d lines, want 1", len(lines))
	}
	if _, ok := attrsOf(lines[0])["tenant"]; !ok {
		t.Errorf("the derived field is missing: %v", attrsOf(lines[0]))
	}
}

// A nil logger leaves the chain untouched rather than panicking on the
// first delivery.
func TestANilLoggerIsAPassThrough(t *testing.T) {
	ran := false
	mw := logging.AccessLog(nil)
	h := mw(events.Subscription{Event: "x.Y"}, func(context.Context, *events.Message) error {
		ran = true
		return nil
	})
	if err := h(context.Background(), &events.Message{Event: "x.Y"}); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Error("a nil logger dropped the delivery")
	}
}
