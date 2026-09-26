package log

import (
	"context"
	"log/slog"
	"strings"
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
	prefix := h.prefix()
	r.Attrs(func(a slog.Attr) bool {
		fields = appendAttr(fields, prefix, a)
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
	prefix := h.prefix()
	for _, a := range attrs {
		out.attrs = appendAttr(out.attrs, prefix, a)
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

// prefix is the key prefix of the open groups, "http." under WithGroup("http").
func (h defaultHandler) prefix() string {
	if len(h.groups) == 0 {
		return ""
	}
	return strings.Join(h.groups, ".") + "."
}

// appendAttr appends a to fields under prefix: a group's attributes flattened under its key
// ("http.status"), an empty-key group inlined, and an empty attribute or group dropped.
func appendAttr(fields []Field, prefix string, a slog.Attr) []Field {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return fields
	}
	if a.Value.Kind() == slog.KindGroup {
		if a.Key != "" {
			prefix += a.Key + "."
		}
		for _, ga := range a.Value.Group() {
			fields = appendAttr(fields, prefix, ga)
		}
		return fields
	}
	if err, ok := a.Value.Any().(error); ok {
		return append(fields, Field{Key: prefix + a.Key, Value: err})
	}
	return append(fields, Field{Key: prefix + a.Key, Value: a.Value.Any()})
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
