package log

import "context"

// Discard returns a Logger that writes nothing; its Enabled reports false.
func Discard() Logger { return discardLogger{} }

type discardLogger struct{}

func (discardLogger) Debug(string, ...Field)               {}
func (discardLogger) Info(string, ...Field)                {}
func (discardLogger) Warn(string, ...Field)                {}
func (discardLogger) Error(string, ...Field)               {}
func (d discardLogger) With(...Field) Logger               { return d }
func (d discardLogger) WithContext(context.Context) Logger { return d }
func (discardLogger) Enabled(Level) bool                   { return false }
