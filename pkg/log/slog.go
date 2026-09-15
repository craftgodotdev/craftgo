package log

import (
	"context"
	"log/slog"
)

// Slog returns a [log/slog.Logger] that writes through craftgo's own
// logger, for a library that speaks slog and a project that has not.
//
// The craftgo logger is resolved on every line rather than captured here.
// A project that installs its own after start-up - `srv.SetLogger(...)`,
// or [SetDefault] - is one whose later lines must go to the new sink, and
// a captured logger would keep writing to the old one. That is worse than
// it sounds when the new logger carries its own level: [SetLevel] would
// still move the HTTP lines and no longer move these.
//
// The cost is one atomic load per line, paid only after the level gate
// has already said yes.
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
	// WithContext is what fans trace_id / span_id into the line, and it
	// is the reason a caller must reach a handler through LogAttrs(ctx,
	// ...) rather than Info(...): slog's level shortcuts pass a
	// background context, so the ids would be dropped with nothing to
	// show for it.
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

// field turns one slog attribute into a craftgo field, qualifying its key
// with any open groups so `WithGroup("http").Int("status", 200)` reads as
// `http.status`.
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

// fromSlogLevel maps a slog level onto craftgo's. The numeric values are
// the same by design, so this is a narrowing rather than a table.
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
