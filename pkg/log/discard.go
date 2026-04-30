package log

import "context"

// Discard returns a Logger that drops every call silently. Useful for
// tests, batch tools, and any runtime where log output is undesirable.
// Wire it via `srv.SetLogger(log.Discard())`.
func Discard() Logger { return discardLogger{} }

// discardLogger is the no-op Logger implementation. Every method is a
// nothing — including the With/WithContext/Enabled surface — so the
// receiver type can be passed through middleware that expects to chain
// further configuration.
type discardLogger struct{}

func (discardLogger) Debug(string, ...Field)               {}
func (discardLogger) Info(string, ...Field)                {}
func (discardLogger) Warn(string, ...Field)                {}
func (discardLogger) Error(string, ...Field)               {}
func (d discardLogger) With(...Field) Logger               { return d }
func (d discardLogger) WithContext(context.Context) Logger { return d }
func (discardLogger) Enabled(Level) bool                   { return false }
