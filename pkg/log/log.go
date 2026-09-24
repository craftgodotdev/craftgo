// Package log is craftgo's structured logging: the [Logger] interface, a zap implementation,
// a process-wide default logger and level, and [Slog], a log/slog bridge.
package log

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Logger is craftgo's structured logger. Beyond a method per level, an implementation:
//   - returns from With a logger that adds fields to every line;
//   - returns from WithContext one that adds the context's trace_id and span_id;
//   - reports from Enabled whether a level is written.
type Logger interface {
	Debug(msg string, fields ...Field)
	Info(msg string, fields ...Field)
	Warn(msg string, fields ...Field)
	Error(msg string, fields ...Field)

	With(fields ...Field) Logger
	WithContext(ctx context.Context) Logger
	Enabled(level Level) bool
}

// Level is a log level; its values equal the matching log/slog levels.
type Level int8

// LevelDebug, LevelInfo, LevelWarn and LevelError are the levels, most verbose first.
const (
	LevelDebug Level = -4
	LevelInfo  Level = 0
	LevelWarn  Level = 4
	LevelError Level = 8
)

// Field is a key and value on a log line.
type Field struct {
	Key   string
	Value any
}

// String returns a string field.
func String(k, v string) Field { return Field{Key: k, Value: v} }

// Int returns an int field.
func Int(k string, v int) Field { return Field{Key: k, Value: v} }

// Int64 returns an int64 field.
func Int64(k string, v int64) Field { return Field{Key: k, Value: v} }

// Float64 returns a float64 field.
func Float64(k string, v float64) Field { return Field{Key: k, Value: v} }

// Bool returns a bool field.
func Bool(k string, v bool) Field { return Field{Key: k, Value: v} }

// Time returns a time field.
func Time(k string, v time.Time) Field { return Field{Key: k, Value: v} }

// Duration returns a duration field.
func Duration(k string, v time.Duration) Field {
	return Field{Key: k, Value: v}
}

// Err returns err under the key "error".
func Err(err error) Field { return Field{Key: "error", Value: err} }

// Any returns a field holding any value.
func Any(k string, v any) Field { return Field{Key: k, Value: v} }

// Group returns a field nesting fs under k.
func Group(k string, fs ...Field) Field { return Field{Key: k, Value: fs} }

// level is the minimum level every New and NewConsole logger shares.
var level = zap.NewAtomicLevelAt(toZapLevel(LevelInfo))

// SetLevel sets the minimum level of every [New] and [NewConsole] logger, from their next
// line on; [NewZap] loggers keep their own. The default is [LevelInfo].
func SetLevel(l Level) { level.SetLevel(toZapLevel(l)) }

// GetLevel returns the level [SetLevel] set.
func GetLevel() Level { return fromZapLevel(level.Level()) }

// ParseLevel parses "debug", "info", "warn" (or "warning") or "error", ignoring case and
// surrounding space; for anything else it returns LevelInfo and false.
func ParseLevel(s string) (Level, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return LevelDebug, true
	case "info":
		return LevelInfo, true
	case "warn", "warning":
		return LevelWarn, true
	case "error":
		return LevelError, true
	default:
		return LevelInfo, false
	}
}

// New returns a Logger writing JSON lines to stderr (zap's production config) at the
// [SetLevel] level.
func New() Logger {
	cfg := zap.NewProductionConfig()
	cfg.Level = level
	z, _ := cfg.Build(zap.AddCallerSkip(1))
	return NewZap(z)
}

// NewConsole returns a Logger writing human-readable lines to stderr (zap's development
// config) at the [SetLevel] level.
func NewConsole() Logger {
	cfg := zap.NewDevelopmentConfig()
	cfg.Level = level
	z, _ := cfg.Build(zap.AddCallerSkip(1))
	return NewZap(z)
}

// NewZap returns a Logger writing to z at z's own level.
func NewZap(z *zap.Logger) Logger { return &zapLogger{z: z} }

// defaultLogger holds the logger [Default] returns.
var defaultLogger atomic.Pointer[Logger]

// SetDefault makes l the logger [Default] returns; nil is ignored.
func SetDefault(l Logger) {
	if l == nil {
		return
	}
	defaultLogger.Store(&l)
}

// Default returns the process-wide logger: a [New] logger until [SetDefault] replaces it.
func Default() Logger { return *defaultLogger.Load() }

func init() { SetDefault(New()) }

// zapLogger is the Logger over a *zap.Logger.
type zapLogger struct{ z *zap.Logger }

// toZap converts f to a zap field; a group nests and a duration is written as text ("1.5ms").
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

func (g fieldsObject) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	for _, f := range g {
		f.AddTo(enc)
	}
	return nil
}

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

func (s *zapLogger) With(fs ...Field) Logger {
	return &zapLogger{z: s.z.With(fieldsToZap(fs)...)}
}

// WithContext adds the trace_id and span_id of ctx's span, when valid, and the
// [SetContextFields] fields.
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
	if fn := contextFields.Load(); fn != nil {
		fields = append(fields, fieldsToZap((*fn)(ctx))...)
	}
	if len(fields) == 0 {
		return s
	}
	return &zapLogger{z: s.z.With(fields...)}
}

// ContextFields derives log fields from a request context, such as a tenant id a middleware
// stored there.
type ContextFields func(ctx context.Context) []Field

// contextFields holds the function [SetContextFields] installed; nil when none is.
var contextFields atomic.Pointer[ContextFields]

// SetContextFields installs fn; the WithContext of [New], [NewConsole] and [NewZap] loggers
// adds the fields it returns to every line. nil removes it.
func SetContextFields(fn ContextFields) {
	if fn == nil {
		contextFields.Store(nil)
		return
	}
	contextFields.Store(&fn)
}

func (s *zapLogger) Enabled(level Level) bool {
	return s.z.Core().Enabled(toZapLevel(level))
}

// toZapLevel maps l to zap's level, rounding up between levels and clamping above LevelError.
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

// fromZapLevel maps l to a Level, clamped to LevelDebug..LevelError.
func fromZapLevel(l zapcore.Level) Level {
	switch {
	case l <= zapcore.DebugLevel:
		return LevelDebug
	case l == zapcore.InfoLevel:
		return LevelInfo
	case l == zapcore.WarnLevel:
		return LevelWarn
	default:
		return LevelError
	}
}
