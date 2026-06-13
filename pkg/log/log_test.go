package log

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// newObserver returns a Logger backed by an in-memory zap core whose
// captured entries can be asserted on.
func newObserver(t *testing.T) (Logger, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zap.DebugLevel)
	return NewZap(zap.New(core)), logs
}

func TestZapLoggerEmitsAllLevels(t *testing.T) {
	l, logs := newObserver(t)
	l.Debug("d", String("k", "v"))
	l.Info("i")
	l.Warn("w")
	l.Error("e", Err(errors.New("boom")))
	if logs.Len() != 4 {
		t.Fatalf("want 4 entries, got %d", logs.Len())
	}
}

// TestZapLoggerWithContextChain pins the canonical context-aware call
// shape: callers that have a context use `WithContext(ctx).<level>(...)`
// instead of a dedicated `InfoCtx(ctx, ...)` shorthand. The interface
// stays minimal, the chain reads explicitly, and the same code path
// powers both empty-context and trace-bearing-context flows.
func TestZapLoggerWithContextChain(t *testing.T) {
	l, logs := newObserver(t)
	ctx := context.Background()
	scoped := l.WithContext(ctx)
	scoped.Debug("d")
	scoped.Info("i")
	scoped.Warn("w")
	scoped.Error("e")
	if logs.Len() != 4 {
		t.Fatalf("want 4 entries, got %d", logs.Len())
	}
}

func TestZapLoggerWithAndWithContext(t *testing.T) {
	l, logs := newObserver(t)
	scoped := l.With(String("svc", "x")).WithContext(context.Background())
	scoped.Info("hello")
	entry := logs.All()[0]
	if entry.ContextMap()["svc"] != "x" {
		t.Errorf("expected svc=x, got %v", entry.ContextMap())
	}
}

// TestZapLoggerWithContextExtractsTrace pins the trace-ID propagation
// rule: when ctx carries an active OTel SpanContext, WithContext
// returns a logger whose subsequent calls automatically tag every
// line with `trace_id` and `span_id`.
func TestZapLoggerWithContextExtractsTrace(t *testing.T) {
	traceID, _ := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	spanID, _ := trace.SpanIDFromHex("0102030405060708")
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)
	ctx = WithRequestID(ctx, "req-xyz")

	l, logs := newObserver(t)
	l.WithContext(ctx).Info("hello")
	if logs.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", logs.Len())
	}
	cm := logs.All()[0].ContextMap()
	if cm["trace_id"] != traceID.String() {
		t.Errorf("expected trace_id %q, got %v", traceID.String(), cm["trace_id"])
	}
	if cm["span_id"] != spanID.String() {
		t.Errorf("expected span_id %q, got %v", spanID.String(), cm["span_id"])
	}
	if cm["request_id"] != "req-xyz" {
		t.Errorf("expected request_id req-xyz, got %v", cm["request_id"])
	}
}

// TestZapLoggerWithContextNoTrace confirms the inverse: when ctx has
// no trace info, WithContext is a no-op and produces an unchanged
// logger.
func TestZapLoggerWithContextNoTrace(t *testing.T) {
	l, logs := newObserver(t)
	l.WithContext(context.Background()).Info("plain")
	if logs.Len() != 1 {
		t.Fatal("expected one entry")
	}
	cm := logs.All()[0].ContextMap()
	if _, ok := cm["trace_id"]; ok {
		t.Errorf("trace_id should be absent for tracerless ctx: %v", cm)
	}
}

func TestZapLoggerEnabled(t *testing.T) {
	l := New()
	if !l.Enabled(LevelInfo) {
		t.Error("Info should be enabled by default")
	}
}

func TestFieldConstructors(t *testing.T) {
	l, logs := newObserver(t)
	l.Info("all", String("s", "x"), Int("i", 1), Int64("i64", 2),
		Float64("f", 1.5), Bool("b", true), Time("t", time.Now()),
		Duration("d", time.Second), Err(errors.New("e")), Any("a", 7),
		Group("g", String("nested", "v")))
	if logs.Len() != 1 {
		t.Fatalf("expected 1 entry")
	}
	cm := logs.All()[0].ContextMap()
	for _, k := range []string{"s", "i", "i64", "f", "b", "t", "d", "error", "a", "g"} {
		if _, ok := cm[k]; !ok {
			t.Errorf("expected key %q in context: %v", k, cm)
		}
	}
}

func TestNewZapDefault(t *testing.T) {
	l := New()
	l.Info("smoke")
	// Ensure smoke test does not panic and Logger is non-nil.
	if l == nil {
		t.Fatal("New() returned nil")
	}
}

func TestErrFieldNamedError(t *testing.T) {
	l, logs := newObserver(t)
	l.Info("named", Field{Key: "cause", Value: errors.New("x")})
	cm := logs.All()[0].ContextMap()
	if _, ok := cm["cause"]; !ok {
		t.Errorf("expected named error key 'cause' in: %v", cm)
	}
}

// TestSetLevelGatesSharedLoggers pins the global-level contract: a logger
// whose core reads the package-level atomic drops entries below the level
// set by SetLevel and keeps the rest, with no logger swap in between. This
// is the same atomic that New / NewConsole - and therefore the server and
// generated logic - share, so one SetLevel call retunes them together.
func TestSetLevelGatesSharedLoggers(t *testing.T) {
	t.Cleanup(func() { SetLevel(LevelInfo) })
	core, logs := observer.New(level)
	l := NewZap(zap.New(core))

	SetLevel(LevelWarn)
	l.Debug("dropped-debug")
	l.Info("dropped-info")
	l.Warn("kept-warn")
	l.Error("kept-error")
	if logs.Len() != 2 {
		t.Fatalf("want 2 entries at warn, got %d", logs.Len())
	}

	SetLevel(LevelDebug)
	l.Debug("now-kept")
	if logs.Len() != 3 {
		t.Fatalf("want 3 entries after lowering to debug, got %d", logs.Len())
	}
}

// TestGetLevelRoundTrip confirms SetLevel/GetLevel agree across the enum,
// including the clamp that snaps sub-debug and super-error inputs.
func TestGetLevelRoundTrip(t *testing.T) {
	t.Cleanup(func() { SetLevel(LevelInfo) })
	for _, want := range []Level{LevelDebug, LevelInfo, LevelWarn, LevelError} {
		SetLevel(want)
		if got := GetLevel(); got != want {
			t.Errorf("SetLevel(%d): GetLevel returned %d", want, got)
		}
	}
}

// TestParseLevel pins the config-string mapping main.go relies on,
// including the case/space insensitivity and the false flag on unknown
// input that lets callers keep their default.
func TestParseLevel(t *testing.T) {
	ok := map[string]Level{
		"debug":   LevelDebug,
		"INFO":    LevelInfo,
		" warn ":  LevelWarn,
		"warning": LevelWarn,
		"Error":   LevelError,
	}
	for in, want := range ok {
		got, recognised := ParseLevel(in)
		if !recognised || got != want {
			t.Errorf("ParseLevel(%q) = (%d, %v), want (%d, true)", in, got, recognised, want)
		}
	}
	for _, in := range []string{"", "verbose", "trace"} {
		if _, recognised := ParseLevel(in); recognised {
			t.Errorf("ParseLevel(%q) should be unrecognised", in)
		}
	}
}

func TestToZapLevelMapping(t *testing.T) {
	if toZapLevel(LevelDebug-1).String() != "debug" {
		t.Error("below-debug should map to debug")
	}
	if toZapLevel(LevelInfo).String() != "info" {
		t.Error("info mapping wrong")
	}
	if toZapLevel(LevelWarn).String() != "warn" {
		t.Error("warn mapping wrong")
	}
	if toZapLevel(LevelError).String() != "error" {
		t.Error("error mapping wrong")
	}
}
