package parser

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

func TestParseConsumeMiddleware(t *testing.T) {
	f := mustParse(t, "package p\nconsume middleware Retry\n")
	md, ok := f.Decls[0].(*ast.MiddlewareDecl)
	if !ok {
		t.Fatalf("want *ast.MiddlewareDecl, got %T", f.Decls[0])
	}
	if md.Name != "Retry" || !md.Consume {
		t.Fatalf("got name=%q consume=%v", md.Name, md.Consume)
	}
	if md.Keyword() != "consume middleware" {
		t.Errorf("Keyword() = %q", md.Keyword())
	}
	// Pos is the `consume` token, not `middleware`, so a diagnostic
	// points at the start of the declaration.
	if md.Pos.Column != 1 {
		t.Errorf("Pos should be the `consume` token, got column %d", md.Pos.Column)
	}
}

func TestParseConsumeWithoutMiddlewareIsAnError(t *testing.T) {
	p := New("t.craftgo", "package p\nconsume Watch { event E }\n")
	p.Parse()
	d := p.Diagnostics()
	if len(d) == 0 {
		t.Fatal("a file-level `consume` block should not parse")
	}
	if !strings.Contains(d[0].Msg, "expected 'middleware' after 'consume'") {
		t.Errorf("unhelpful diagnostic: %q", d[0].Msg)
	}
}

func TestParseConsumeMiddlewareKeepsDecorators(t *testing.T) {
	f := mustParse(t, "package p\n@doc(\"x\")\nconsume middleware Retry\n")
	md := f.Decls[0].(*ast.MiddlewareDecl)
	if len(md.Decorators) != 1 || md.Decorators[0].Name != "doc" {
		t.Fatalf("decorators lost: %v", md.Decorators)
	}
	if !md.Consume {
		t.Error("Consume lost when decorators are present")
	}
}

// A plain `middleware` declaration keeps its old shape.
func TestParsePlainMiddlewareIsNotConsume(t *testing.T) {
	f := mustParse(t, "package p\nmiddleware Auth\n")
	md := f.Decls[0].(*ast.MiddlewareDecl)
	if md.Consume {
		t.Error("plain middleware marked as consume")
	}
	if md.Keyword() != "middleware" {
		t.Errorf("Keyword() = %q", md.Keyword())
	}
}
