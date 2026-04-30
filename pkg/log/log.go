// Package log defines the Logger interface used across craftgo together
// with a default zap adapter. Projects that already standardised on slog
// or zerolog can satisfy the same interface with their own adapter.
package log

import (
	"context"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Logger is the small structured-logging surface every craftgo middleware
// depends on. Callers that have a request context use
// `logger.WithContext(ctx).Info(...)` to fan trace_id / span_id /
// request_id into the line; callers without a context call `Info(...)`
// directly. There is intentionally no `InfoCtx(ctx, ...)` shorthand —
// the chain reads explicitly and keeps the interface minimal.
type Logger interface {
	Debug(msg string, fields ...Field)
	Info(msg string, fields ...Field)
	Warn(msg string, fields ...Field)
	Error(msg string, fields ...Field)

	With(fields ...Field) Logger
	WithContext(ctx context.Context) Logger
	Enabled(level Level) bool
}

// Level is a coarse level enum that maps onto zap's atomic level. Values
// align with the standard slog levels so external adapters translate
// without surprises.
type Level int8

// Level constants. The numeric gaps mirror slog (-4/0/4/8) so adapter
// authors can copy the conversion table verbatim.
const (
	LevelDebug Level = -4
	LevelInfo  Level = 0
	LevelWarn  Level = 4
	LevelError Level = 8
)

// Field is a typed key/value pair. Use the constructors below; building a
// literal works but skips the typing benefit.
type Field struct {
	Key   string
	Value any
}

// String / Int / Int64 / Float64 / Bool / Time / Duration / Err / Any /
// Group are the canonical Field constructors documented in the README.
func String(k, v string) Field          { return Field{Key: k, Value: v} }
func Int(k string, v int) Field         { return Field{Key: k, Value: v} }
func Int64(k string, v int64) Field     { return Field{Key: k, Value: v} }
func Float64(k string, v float64) Field { return Field{Key: k, Value: v} }
func Bool(k string, v bool) Field       { return Field{Key: k, Value: v} }
func Time(k string, v time.Time) Field  { return Field{Key: k, Value: v} }
func Duration(k string, v time.Duration) Field {
	return Field{Key: k, Value: v}
}
func Err(err error) Field               { return Field{Key: "error", Value: err} }
func Any(k string, v any) Field         { return Field{Key: k, Value: v} }
func Group(k string, fs ...Field) Field { return Field{Key: k, Value: fs} }

// New returns the default Logger: a production-configured zap logger
// writing JSON to stderr.
func New() Logger {
	z, _ := zap.NewProduction(zap.AddCallerSkip(1))
	return NewZap(z)
}

// NewConsole returns a development-configured Logger that emits
// human-readable, colour-tagged lines to stderr. Same Logger contract
// as [New] — drop into `srv.SetLogger(log.NewConsole())` for local
// `go run` sessions and switch back to [New] for production.
func NewConsole() Logger {
	z, _ := zap.NewDevelopment(zap.AddCallerSkip(1))
	return NewZap(z)
}

// NewZap wraps an existing `*zap.Logger` so projects that already
// configured zap can reuse it.
func NewZap(z *zap.Logger) Logger { return &zapLogger{z: z} }

// defaultLogger is the package-level Logger that callers without an
// explicit instance reach for via [Default]. The atomic.Value lets
// runtime swaps (e.g. `Server.SetLogger`) take effect immediately
// without locks. The initial value — assigned in init() — is a
// production zap so a fresh import is silently usable.
var defaultLogger atomic.Value

// SetDefault swaps the package-level Logger. Server.SetLogger calls
// this so codegen-emitted logic files can read the same instance via
// [Default] without a constructor parameter or context lookup.
// Passing nil is a no-op.
func SetDefault(l Logger) {
	if l == nil {
		return
	}
	defaultLogger.Store(loggerHolder{l})
}

// Default returns the current package-level Logger. Generated logic
// constructors read it (typically chained with `.WithContext(ctx)`)
// so user code can call `l.Info(...)` directly without juggling
// context plumbing.
func Default() Logger {
	if v := defaultLogger.Load(); v != nil {
		return v.(loggerHolder).Logger
	}
	return New()
}

// loggerHolder is a tiny value wrapper so atomic.Value sees a
// consistent concrete type across stores (it forbids storing
// values of different concrete types).
type loggerHolder struct{ Logger }

func init() {
	SetDefault(New())
}

// zapLogger is the default Logger implementation. It maps Field values
// onto zap's strongly-typed `zap.Field` constructors.
type zapLogger struct{ z *zap.Logger }

// toZap converts a single craftgo Field into a zap.Field. Group fields
// recurse so nested structures preserve their shape in the output.
//
// time.Duration values are rendered through `Duration.String()`
// ("1.5ms", "250µs", "5s") rather than the default zap behaviour of
// "fractional seconds" — the human-readable form is the right
// default for log lines a person actually reads (access logs,
// op-error breadcrumbs). Code that needs the numeric form for
// dashboards / alerts should record the duration as a metric (a
// Histogram on the OTel meter) instead of a log field.
func toZap(f Field) zap.Field {
	switch v := f.Value.(type) {
	case []Field:
		nested := make([]zap.Field, 0, len(v))
		for _, n := range v {
			nested = append(nested, toZap(n))
		}
		return zap.Object(f.Key, fieldsObject(nested))
	case time.Duration:
		return zap.String(f.Key, v.String())
	case error:
		if f.Key == "" || f.Key == "error" {
			return zap.Error(v)
		}
		return zap.NamedError(f.Key, v)
	default:
		return zap.Any(f.Key, v)
	}
}

// fieldsObject implements zapcore.ObjectMarshaler for nested groups.
type fieldsObject []zap.Field

// MarshalLogObject writes each nested field through the supplied encoder.
func (g fieldsObject) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	for _, f := range g {
		f.AddTo(enc)
	}
	return nil
}

// fieldsToZap converts the variadic Field slice into zap's []zap.Field.
func fieldsToZap(fs []Field) []zap.Field {
	out := make([]zap.Field, 0, len(fs))
	for _, f := range fs {
		out = append(out, toZap(f))
	}
	return out
}

func (s *zapLogger) Debug(msg string, fs ...Field) { s.z.Debug(msg, fieldsToZap(fs)...) }
func (s *zapLogger) Info(msg string, fs ...Field)  { s.z.Info(msg, fieldsToZap(fs)...) }
func (s *zapLogger) Warn(msg string, fs ...Field)  { s.z.Warn(msg, fieldsToZap(fs)...) }
func (s *zapLogger) Error(msg string, fs ...Field) { s.z.Error(msg, fieldsToZap(fs)...) }

// Ctx-suffixed methods currently delegate to the non-ctx variants. Once
func (s *zapLogger) With(fs ...Field) Logger {
	return &zapLogger{z: s.z.With(fieldsToZap(fs)...)}
}
// WithContext extracts the active OpenTelemetry trace IDs and the
// X-Request-Id stored by the RequestID middleware from ctx, then
// returns a Logger with those fields baked in. Subsequent calls on the
// returned Logger automatically tag every line with `trace_id`,
// `span_id`, and `request_id` — the standard observability triple.
//
// When ctx carries no trace context (test runs, batch tools) the
// trace fields are simply omitted from the output.
func (s *zapLogger) WithContext(ctx context.Context) Logger {
	if ctx == nil {
		return s
	}
	var fields []zap.Field
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		fields = append(fields,
			zap.String("trace_id", sc.TraceID().String()),
			zap.String("span_id", sc.SpanID().String()),
		)
	}
	if id := requestIDFromContext(ctx); id != "" {
		fields = append(fields, zap.String("request_id", id))
	}
	if len(fields) == 0 {
		return s
	}
	return &zapLogger{z: s.z.With(fields...)}
}

// requestIDKey is the unexported context-key type used by
// [WithRequestID] / [requestIDFromContext]. Keeping the type private
// stops third-party code from accidentally colliding with our key.
type requestIDKey struct{}

// WithRequestID returns ctx with the supplied request id stashed under
// the package's canonical key. pkg/server.RequestID calls this so
// `log.WithContext(ctx)` can pick the value up without taking a hard
// dependency on pkg/server.
func WithRequestID(ctx context.Context, id string) context.Context {
	if ctx == nil || id == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDKey{}, id)
}

// requestIDFromContext is the inverse of [WithRequestID]. Returns ""
// when the ctx wasn't tagged.
func requestIDFromContext(ctx context.Context) string {
	if v := ctx.Value(requestIDKey{}); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func (s *zapLogger) Enabled(level Level) bool {
	return s.z.Core().Enabled(toZapLevel(level))
}

// toZapLevel translates the public Level enum to zap's level type.
func toZapLevel(l Level) zapcore.Level {
	switch {
	case l <= LevelDebug:
		return zapcore.DebugLevel
	case l <= LevelInfo:
		return zapcore.InfoLevel
	case l <= LevelWarn:
		return zapcore.WarnLevel
	default:
		return zapcore.ErrorLevel
	}
}
