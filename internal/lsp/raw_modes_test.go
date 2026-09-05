package lsp

import (
	"strings"
	"testing"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// The raw-mode decorators reach the editor through the registry alone:
// completion, hover and diagnostics need no LSP-side knowledge of them.
// These tests pin that the registry entries are wired the way an editor
// user sees them.

func TestCompletionRawModeFlagsAtMethodSite(t *testing.T) {
	src := "package x\n\nservice S {\n\t@\n\tget A /a {}\n}\n"
	// Cursor right after the `@` on the method line (0-indexed line 3).
	items := mustCompletionsAt(t, "t.craftgo", src, 3, 2)
	expectLabels(t, items, "rawRequest", "rawResponse", "passthrough")
	for _, it := range items {
		switch it.Label {
		case "rawRequest", "rawResponse", "passthrough":
			// Flags insert bare - no `($0)` snippet to delete.
			if it.InsertText != it.Label {
				t.Errorf("@%s must insert without parens, got %q", it.Label, it.InsertText)
			}
			if !strings.Contains(it.Documentation.(string), "docs-only contract") {
				t.Errorf("@%s documentation must explain the docs-only contract, got %q", it.Label, it.Documentation)
			}
		}
	}
	// Method-level only: the flags must not show up at a field site.
	fieldItems := mustCompletionsAt(t, "t.craftgo", "package x\n\ntype T {\n\tid string @\n}\n", 3, 12)
	expectNoLabels(t, fieldItems, "rawRequest", "rawResponse", "passthrough")
}

func TestHoverRawModeFlags(t *testing.T) {
	src := "package x\ntype Req { id string @path }\nservice S {\n\t@rawResponse\n\tget A /a/{id} { request Req }\n}\n"
	v := mustHoverAt(t, "t.craftgo", src, "rawResponse")
	for _, want := range []string{"@rawResponse", "method", "http.ResponseWriter"} {
		if !strings.Contains(v, want) {
			t.Errorf("hover must mention %q, got %q", want, v)
		}
	}
}

func TestDiagnosticsRawModeRedundancySurfacesAsWarning(t *testing.T) {
	src := "package x\nservice S {\n\t@passthrough\n\t@rawRequest\n\tget A /a {}\n}\n"
	got := newTestServer().buildDiagnostics(uri.New("file:///t.craftgo"), src)
	var found *protocol.Diagnostic
	for i := range got {
		if c, _ := got[i].Code.(string); c == "decorator/redundant" {
			found = &got[i]
		}
	}
	if found == nil {
		t.Fatalf("expected decorator/redundant, got %+v", got)
	}
	if found.Severity != protocol.DiagnosticSeverityWarning {
		t.Errorf("severity = %v, want warning", found.Severity)
	}
	if found.Range.Start.Line != 3 {
		t.Errorf("warning must anchor on the later decorator (line 3), got line %d", found.Range.Start.Line)
	}
	if len(found.RelatedInformation) != 1 || found.RelatedInformation[0].Location.Range.Start.Line != 2 {
		t.Errorf("related information must point at @passthrough on line 2, got %+v", found.RelatedInformation)
	}
}

func TestDiagnosticsRawModeBlocksAreClean(t *testing.T) {
	src := "package x\ntype Req { id string @path }\ntype Resp { ok bool }\nservice S {\n\t@rawResponse\n\tget A /a/{id} { request Req  response Resp }\n\t@rawRequest\n\tpost B /b { response Resp }\n\t@passthrough\n\tget C /c/{id} { request Req  response Resp }\n}\n"
	if got := newTestServer().buildDiagnostics(uri.New("file:///t.craftgo"), src); len(got) != 0 {
		t.Fatalf("blocks on raw sides must not produce diagnostics, got %+v", got)
	}
}
