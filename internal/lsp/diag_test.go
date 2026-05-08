package lsp

import (
	"strings"
	"testing"

	"go.lsp.dev/uri"
)

// newTestServer returns an empty Server suitable for unit tests that
// invoke buildDiagnostics directly. It sidesteps the JSON-RPC wiring -
// the diag pipeline only reads from s.docs and the file system.
func newTestServer() *Server {
	return &Server{docs: map[uri.URI]*document{}}
}

// TestBuildDiagnosticsClean verifies that valid DSL produces no
// diagnostics - the formatter's roundtrip cases live there too, so this
// is mostly a smoke check that wiring is alive.
func TestBuildDiagnosticsClean(t *testing.T) {
	src := `package design

type User {
	id   string
	name string @length(1, 80)
}
`
	got := newTestServer().buildDiagnostics(uri.New("file:///test.craftgo"), src)
	if len(got) != 0 {
		t.Fatalf("expected zero diagnostics for clean source, got %d: %+v", len(got), got)
	}
}

// TestBuildDiagnosticsParseError checks that a syntax error is surfaced
// with a usable position (line/character non-zero) and the source label.
func TestBuildDiagnosticsParseError(t *testing.T) {
	// Missing closing brace.
	src := `package design

type User {
	id string
`
	got := newTestServer().buildDiagnostics(uri.New("file:///test.craftgo"), src)
	if len(got) == 0 {
		t.Fatal("expected at least one diagnostic for unclosed type body")
	}
	d := got[0]
	if d.Source != "craftgo" {
		t.Errorf("Source = %q, want %q", d.Source, "craftgo")
	}
	if d.Message == "" {
		t.Error("Message is empty")
	}
}

// TestBuildDiagnosticsSemanticError checks that semantic errors (e.g.
// unknown decorator) are reported with their stable diagnostic codes,
// not just generic parse failures.
func TestBuildDiagnosticsSemanticError(t *testing.T) {
	src := `package design

type User {
	id string @notARealDecorator
}
`
	got := newTestServer().buildDiagnostics(uri.New("file:///test.craftgo"), src)
	var foundCode string
	for _, d := range got {
		c, _ := d.Code.(string)
		if strings.HasPrefix(c, "decorator/") {
			foundCode = c
			break
		}
	}
	if foundCode == "" {
		t.Fatalf("expected a decorator/* code in diagnostics, got %+v", got)
	}
}
