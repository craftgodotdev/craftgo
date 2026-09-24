package log

import (
	"context"
	"log/slog"
)

// Slog returns a [log/slog.Logger] that writes through [Default], resolved on every line so
// a later [SetDefault] applies. Its context methods (InfoContext, LogAttrs) carry trace ids.
func Slog() *slog.Logger { return slog.New(defaultHandler{}) }

// defaultHandler is a [log/slog.Handler] over whatever [Default] returns
// at the moment a record is handled.
type defaultHandler struct {
	attrs  []Field
	groups []string
}

func (h defaultHandler) Enabled(_ context.Context, level slog.Level) bool {
	return Default().Enabled(fromSlogLevel(level))
}

func (h defaultHandler) Handle(ctx context.Context, r slog.Record) error {
	l := Default().WithContext(ctx)

	fields := make([]Field, 0, len(h.attrs)+r.NumAttrs())
	fields = append(fields, h.attrs...)
	r.Attrs(func(a slog.Attr) bool {
		fields = append(fields, h.field(a))
		return true
	})

	switch {
	case r.Level >= slog.LevelError:
		l.Error(r.Message, fields...)
	case r.Level >= slog.LevelWarn:
		l.Warn(r.Message, fields...)
	case r.Level >= slog.LevelInfo:
		l.Info(r.Message, fields...)
	default:
		l.Debug(r.Message, fields...)
	}
	return nil
}

func (h defaultHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	out := defaultHandler{attrs: make([]Field, 0, len(h.attrs)+len(attrs)), groups: h.groups}
	out.attrs = append(out.attrs, h.attrs...)
	for _, a := range attrs {
		out.attrs = append(out.attrs, h.field(a))
	}
	return out
}

func (h defaultHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	groups := make([]string, 0, len(h.groups)+1)
	groups = append(groups, h.groups...)
	return defaultHandler{attrs: h.attrs, groups: append(groups, name)}
}

// field converts a to a Field, prefixing its key with the open groups ("http.status").
func (h defaultHandler) field(a slog.Attr) Field {
	key := a.Key
	for i := len(h.groups) - 1; i >= 0; i-- {
		key = h.groups[i] + "." + key
	}
	if err, ok := a.Value.Any().(error); ok {
		return Field{Key: key, Value: err}
	}
	return Field{Key: key, Value: a.Value.Resolve().Any()}
}

// fromSlogLevel rounds a slog level down to the nearest Level, at least LevelDebug.
func fromSlogLevel(l slog.Level) Level {
	switch {
	case l >= slog.LevelError:
		return LevelError
	case l >= slog.LevelWarn:
		return LevelWarn
	case l >= slog.LevelInfo:
		return LevelInfo
	default:
		return LevelDebug
	}
}
