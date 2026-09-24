package log_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/log"
)

// recorder is a craftgo Logger that remembers what it was asked to write.
type recorder struct {
	mu     sync.Mutex
	lines  []string
	fields [][]log.Field
	ctxs   []context.Context
}

func (r *recorder) write(msg string, fields []log.Field) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, msg)
	r.fields = append(r.fields, fields)
}

func (r *recorder) Debug(msg string, f ...log.Field) { r.write(msg, f) }
func (r *recorder) Info(msg string, f ...log.Field)  { r.write(msg, f) }
func (r *recorder) Warn(msg string, f ...log.Field)  { r.write(msg, f) }
func (r *recorder) Error(msg string, f ...log.Field) { r.write(msg, f) }
func (r *recorder) With(...log.Field) log.Logger     { return r }
func (r *recorder) Enabled(log.Level) bool           { return true }

func (r *recorder) WithContext(ctx context.Context) log.Logger {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ctxs = append(r.ctxs, ctx)
	return r
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.lines)
}

// Slog writes to a logger installed after it was created.
func TestSlogResolvesTheDefaultLoggerPerLine(t *testing.T) {
	restore := log.Default()
	t.Cleanup(func() { log.SetDefault(restore) })

	// Created before SetDefault.
	captured := log.Slog()

	rec := &recorder{}
	log.SetDefault(rec)
	captured.LogAttrs(context.Background(), slog.LevelInfo, "after")

	if rec.count() != 1 {
		t.Fatalf("the recorder saw %d lines, want 1 - Slog captured a logger instead of resolving one", rec.count())
	}
	if rec.lines[0] != "after" {
		t.Errorf("line = %q", rec.lines[0])
	}
}

// The record's context reaches the logger's WithContext.
func TestSlogPassesTheContextThrough(t *testing.T) {
	restore := log.Default()
	t.Cleanup(func() { log.SetDefault(restore) })

	type ctxKey struct{}
	rec := &recorder{}
	log.SetDefault(rec)

	ctx := context.WithValue(context.Background(), ctxKey{}, "trace-42")
	log.Slog().LogAttrs(ctx, slog.LevelInfo, "line")

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.ctxs) != 1 {
		t.Fatalf("WithContext called %d times, want 1", len(rec.ctxs))
	}
	if got := rec.ctxs[0].Value(ctxKey{}); got != "trace-42" {
		t.Errorf("the logger got context value %v, want trace-42", got)
	}
}

// Slog carries both With and call attributes.
func TestSlogCarriesAttributesAndLevels(t *testing.T) {
	restore := log.Default()
	t.Cleanup(func() { log.SetDefault(restore) })

	rec := &recorder{}
	log.SetDefault(rec)

	l := log.Slog().With(slog.String("service", "orders"))
	l.LogAttrs(context.Background(), slog.LevelWarn, "careful", slog.Int("count", 3))

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.fields) != 1 {
		t.Fatalf("wrote %d lines, want 1", len(rec.fields))
	}
	got := map[string]bool{}
	for _, f := range rec.fields[0] {
		got[f.Key] = true
	}
	if !got["service"] || !got["count"] {
		t.Errorf("fields = %v, want the With attribute and the call attribute", rec.fields[0])
	}
}

// A group prefixes the keys under it.
func TestSlogQualifiesGroupedKeys(t *testing.T) {
	restore := log.Default()
	t.Cleanup(func() { log.SetDefault(restore) })

	rec := &recorder{}
	log.SetDefault(rec)
	log.Slog().WithGroup("http").LogAttrs(context.Background(), slog.LevelInfo, "req", slog.Int("status", 200))

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.fields) != 1 || len(rec.fields[0]) != 1 {
		t.Fatalf("fields = %v", rec.fields)
	}
	if got := rec.fields[0][0].Key; got != "http.status" {
		t.Errorf("key = %q, want http.status", got)
	}
}
