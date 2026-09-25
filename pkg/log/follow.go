package log

import (
	"context"
	"slices"
)

// Follow returns a Logger that writes each line through [Default] as it is at that line, so a
// later [SetDefault] reaches it, and the loggers its With and WithContext return, which apply
// their fields and context anew on each line.
func Follow() Logger { return followDefault }

// followDefault is the [Follow] logger that adds nothing.
var followDefault = &follower{}

// follower is the [Follow] logger: WithContext(ctx) when ctx is set, else With(fields...),
// applied at each line after its parent's steps; the root has neither.
type follower struct {
	parent *follower
	ctx    context.Context
	fields []Field
}

// callerSkipper is a Logger that can name as the caller of its lines the code one frame out.
type callerSkipper interface{ skipCaller() Logger }

// current returns [Default] with f's steps applied, naming the caller of f's method as the
// caller of its lines.
func (f *follower) current() Logger {
	l := Default()
	if s, ok := l.(callerSkipper); ok {
		l = s.skipCaller()
	}
	return f.on(l)
}

// on returns l with f's steps applied.
func (f *follower) on(l Logger) Logger {
	if f.parent == nil {
		return l
	}
	l = f.parent.on(l)
	if f.ctx != nil {
		return l.WithContext(f.ctx)
	}
	return l.With(f.fields...)
}

func (f *follower) Debug(msg string, fs ...Field) { f.current().Debug(msg, fs...) }
func (f *follower) Info(msg string, fs ...Field)  { f.current().Info(msg, fs...) }
func (f *follower) Warn(msg string, fs ...Field)  { f.current().Warn(msg, fs...) }
func (f *follower) Error(msg string, fs ...Field) { f.current().Error(msg, fs...) }
func (f *follower) Enabled(l Level) bool          { return Default().Enabled(l) }

// With folds fs into f's own With step, so a chain of With calls costs one With a line.
func (f *follower) With(fs ...Field) Logger {
	if len(fs) == 0 {
		return f
	}
	if f.parent != nil && f.ctx == nil {
		return &follower{parent: f.parent, fields: append(slices.Clip(f.fields), fs...)}
	}
	return &follower{parent: f, fields: slices.Clone(fs)}
}

// WithContext adds nothing for a nil ctx, as the zap loggers do.
func (f *follower) WithContext(ctx context.Context) Logger {
	if ctx == nil {
		return f
	}
	return &follower{parent: f, ctx: ctx}
}
