package log

import (
	"context"
	"io"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// swapDefault installs l as the default until the test ends.
func swapDefault(t *testing.T, l Logger) {
	t.Helper()
	prev := Default()
	SetDefault(l)
	t.Cleanup(func() { SetDefault(prev) })
}

// A Follow logger, and the loggers its With and WithContext return, write each line to the
// Default of that moment, with their fields and the context's trace ids.
func TestFollow(t *testing.T) {
	first, firstLogs := newObserver(t)
	swapDefault(t, first)
	f := Follow()
	tagged := f.With(String("component", "x"))
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:  trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
	})
	traced := tagged.WithContext(trace.ContextWithSpanContext(context.Background(), sc))
	f.Info("before")

	second, secondLogs := newObserver(t)
	SetDefault(second)
	f.Info("after")
	tagged.Warn("tagged")
	traced.Error("traced")

	if firstLogs.Len() != 1 || firstLogs.All()[0].Message != "before" {
		t.Errorf("first default got %v, want the line written before the switch", firstLogs.All())
	}
	lines := secondLogs.All()
	if len(lines) != 3 {
		t.Fatalf("second default got %d lines, want 3", len(lines))
	}
	if got := lines[1].ContextMap()["component"]; got != "x" || lines[1].Level != zapcore.WarnLevel {
		t.Errorf("With line = %v %v, want Warn with component x", lines[1].Level, lines[1].ContextMap())
	}
	fields := lines[2].ContextMap()
	if fields["component"] != "x" || fields["trace_id"] != sc.TraceID().String() || fields["span_id"] != sc.SpanID().String() {
		t.Errorf("WithContext line fields = %v, want component and the trace ids", fields)
	}
	if !f.Enabled(LevelDebug) {
		t.Error("Enabled must report the current default's level")
	}
}

// Two loggers With derives from one keep their own fields.
func TestFollowWithKeepsSiblingsApart(t *testing.T) {
	l, logs := newObserver(t)
	swapDefault(t, l)
	base := Follow().With(String("a", "1"), String("b", "2"))
	base.With(String("c", "3")).Info("c")
	base.With(String("d", "4")).Info("d")
	for _, e := range logs.All() {
		fields := e.ContextMap()
		if _, both := fields["c"]; both && fields["d"] != nil {
			t.Errorf("%s carries its sibling's field: %v", e.Message, fields)
		}
		if fields["a"] != "1" || fields["b"] != "2" {
			t.Errorf("%s lost the base fields: %v", e.Message, fields)
		}
	}
}

// A line through a Follow logger reports the code that wrote it as its caller, as a line
// through Default does.
func TestFollowReportsTheCaller(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	swapDefault(t, NewZap(zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))))
	Default().Info("default")
	Follow().Info("follow")
	Follow().With(String("k", "v")).WithContext(context.Background()).Warn("chained")
	for _, e := range logs.All() {
		if !strings.HasSuffix(e.Caller.File, "follow_test.go") {
			t.Errorf("%s: caller %s, want this test file", e.Message, e.Caller.File)
		}
	}
}

// compare reports a == b, or the panic comparing them raised.
func compare(a, b Logger) (equal bool, panicked any) {
	defer func() { panicked = recover() }()
	return a == b, nil
}

// Follow loggers compare, and key a map, as the loggers they replace did.
func TestFollowLoggersCompare(t *testing.T) {
	if equal, panicked := compare(Follow(), Follow()); !equal || panicked != nil {
		t.Errorf("Follow() == Follow(): %v, panic %v; want true", equal, panicked)
	}
	tagged := Follow().With(String("k", "v"))
	if equal, panicked := compare(tagged, tagged); !equal || panicked != nil {
		t.Errorf("a With logger compared to itself: %v, panic %v; want true", equal, panicked)
	}
	func() {
		defer func() {
			if p := recover(); p != nil {
				t.Errorf("a Follow logger as a map key: %v", p)
			}
		}()
		_ = map[Logger]bool{tagged: true}
	}()
}

// WithContext(nil) adds nothing, so an earlier context's trace ids stay.
func TestFollowWithContextNilKeepsTheContext(t *testing.T) {
	l, logs := newObserver(t)
	swapDefault(t, l)
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:  trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
	})
	var noCtx context.Context
	Follow().WithContext(trace.ContextWithSpanContext(context.Background(), sc)).WithContext(noCtx).Info("x")
	if got := logs.All()[0].ContextMap()["trace_id"]; got != sc.TraceID().String() {
		t.Errorf("trace_id = %v, want the earlier context's", got)
	}
}

// SetDefault given a Follow logger installs the logger that one writes through, its With
// fields included, so one server's Logger can be another's SetLogger.
func TestSetDefaultTakesAFollowLogger(t *testing.T) {
	l, logs := newObserver(t)
	swapDefault(t, l)
	SetDefault(Follow())
	SetDefault(Follow().With(String("svc", "a")))
	if _, ok := Default().(*follower); ok {
		t.Fatal("Default is a Follow logger, which would write through itself")
	}
	Default().Info("x")
	if logs.Len() != 1 {
		t.Fatalf("lines on the logger Follow wrote through = %d, want 1", logs.Len())
	}
	if got := logs.All()[0].ContextMap()["svc"]; got != "a" {
		t.Errorf("svc = %v, want the With field", got)
	}
}

// A Follow line allocates at most once more than the same line through Default, and a chain
// of With calls costs a line what one With of all their fields costs.
func TestFollowLinesCostAboutWhatDefaultLinesCost(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts vary between runs under the race detector")
	}
	enc := zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
	swapDefault(t, NewZap(zap.New(zapcore.NewCore(enc, zapcore.AddSync(io.Discard), zap.DebugLevel), zap.AddCaller())))
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:  trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	direct := testing.AllocsPerRun(100, func() { Default().WithContext(ctx).Info("x", String("k", "v")) })
	follow := testing.AllocsPerRun(100, func() { Follow().WithContext(ctx).Info("x", String("k", "v")) })
	if follow > direct+1 {
		t.Errorf("a Follow line allocated %.0f times, the same line through Default %.0f; want at most one more", follow, direct)
	}

	chain := Follow().With(Int("a", 1)).With(Int("b", 2)).With(Int("c", 3))
	merged := Follow().With(Int("a", 1), Int("b", 2), Int("c", 3))
	chained := testing.AllocsPerRun(100, func() { chain.Info("x") })
	once := testing.AllocsPerRun(100, func() { merged.Info("x") })
	if chained != once {
		t.Errorf("a line through three With calls allocated %.0f times, through one With of their fields %.0f", chained, once)
	}
}

// wrapping is a Logger that decorates another, as a user wraps srv.Logger().
type wrapping struct{ Logger }

// A default that writes through a Follow logger does not send Follow lines back into itself:
// its own lines and Follow lines both reach the logger beneath it.
func TestSetDefaultWithAWrappedFollowLogger(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	swapDefault(t, NewZap(zap.New(core)))
	SetDefault(wrapping{Follow().With(String("via", "wrapper"))})
	Default().Info("direct")
	Follow().Info("followed")
	if Default().Enabled(LevelDebug) {
		t.Error("Enabled answers from the logger beneath, which is at info")
	}
	if logs.Len() != 2 || logs.All()[0].Message != "direct" || logs.All()[1].Message != "followed" {
		t.Errorf("lines = %v, want direct then followed", logs.All())
	}
}
