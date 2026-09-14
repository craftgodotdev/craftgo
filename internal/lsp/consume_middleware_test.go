package lsp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// consumeMiddlewareFixture writes a design declaring one middleware of
// each kind plus a consumer to attach them to, and returns the parsed
// view together with the server and URI a completion needs.
func consumeMiddlewareFixture(t *testing.T, src string) (snapshotView, *Server, string, string) {
	t.Helper()
	root := t.TempDir()
	srcPath := filepath.Join(root, "t.craftgo")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	return parseSnapshot(srcPath, src), &Server{docs: map[uri.URI]*document{}}, string(uri.File(srcPath)), src
}

// The two tables are separate, so the popup must be too: offering an
// HTTP middleware inside `@consumeMiddlewares(` would propose a name
// semantic then refuses with middleware/kind-mismatch, which is worse
// than not offering it.
func TestCompletionConsumeMiddlewareOffersOnlyConsumeKind(t *testing.T) {
	src := `package x

middleware Auth

consume middleware Retry

type P { id string }

service Orders { event Placed { payload P } }

service Watchers {
@consumeMiddlewares(
	consume Watch { event Placed }
}
`
	view, srv, fileURI, source := consumeMiddlewareFixture(t, src)
	// Cursor right after `@consumeMiddlewares(`.
	pos := protocol.Position{Line: 11, Character: 20}
	got := map[string]bool{}
	for _, it := range srv.completionsAt(view, pos, fileURI, source) {
		got[it.Label] = true
	}
	if !got["Retry"] {
		t.Errorf("@consumeMiddlewares( did not offer the consume middleware Retry; got %v", keys2(got))
	}
	if got["Auth"] {
		t.Errorf("@consumeMiddlewares( offered the HTTP middleware Auth; got %v", keys2(got))
	}
}

// The mirror: `@middlewares(` must not offer a consume middleware.
func TestCompletionMiddlewareOffersOnlyHTTPKind(t *testing.T) {
	src := `package x

middleware Auth

consume middleware Retry

type Pong { ok bool }

service S {
@middlewares(
	get Ping / { response Pong }
}
`
	view, srv, fileURI, source := consumeMiddlewareFixture(t, src)
	// Cursor right after `@middlewares(`.
	pos := protocol.Position{Line: 9, Character: 13}
	got := map[string]bool{}
	for _, it := range srv.completionsAt(view, pos, fileURI, source) {
		got[it.Label] = true
	}
	if !got["Auth"] {
		t.Errorf("@middlewares( did not offer the HTTP middleware Auth; got %v", keys2(got))
	}
	if got["Retry"] {
		t.Errorf("@middlewares( offered the consume middleware Retry; got %v", keys2(got))
	}
}

// Go-to-definition inside `@consumeMiddlewares(...)` resolves through
// the consume table. One name can only be one kind
// (middleware/collision spans both), so the assertion is that it lands
// on the consume declaration at all rather than missing.
func TestDefinitionInsideConsumeMiddlewares(t *testing.T) {
	src := `package x

consume middleware Retry

type P { id string }

service Orders { event Placed { payload P } }

service Watchers {
	@consumeMiddlewares(Retry)
	consume Watch { event Placed }
}
`
	view := parseSnapshot("t.craftgo", src)
	// The Retry token INSIDE the decorator is the second occurrence.
	var inside protocol.Position
	count := 0
	for _, tok := range view.tokens {
		if tok.Text != "Retry" {
			continue
		}
		count++
		if count == 2 {
			inside = protocol.Position{Line: uint32(tok.Pos.Line - 1), Character: uint32(tok.Pos.Column - 1)}
			break
		}
	}
	if count < 2 {
		t.Fatalf("expected 2 Retry tokens, got %d", count)
	}
	decName, ok := decoratorArgContext(view, inside)
	if !ok || decName != "consumeMiddlewares" {
		t.Fatalf("decoratorArgContext = %q ok=%v, want consumeMiddlewares", decName, ok)
	}
	if kind := lookupKindAt(view, 0, inside); kind != semantic.ConsumeMiddlewareDecls {
		t.Errorf("lookupKindAt = %v, want ConsumeMiddlewareDecls", kind)
	}
	d := lookupIn(t, "x", "Retry", semantic.ConsumeMiddlewareDecls, view.file)
	if d == nil {
		t.Fatal("consume middleware lookup returned nil")
	}
	md, isMW := d.(*ast.MiddlewareDecl)
	if !isMW {
		t.Fatalf("expected *ast.MiddlewareDecl, got %T", d)
	}
	if !md.Consume {
		t.Error("resolved to the HTTP table, not the consume one")
	}
}

// keys2 renders a label set for a failure message.
func keys2(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
