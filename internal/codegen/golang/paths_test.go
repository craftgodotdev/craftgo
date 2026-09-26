package golang

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/route"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

func TestRelDirNormalises(t *testing.T) {
	for in, want := range map[string]string{
		"":                   "",
		".":                  "",
		"./":                 "",
		"./internal/config/": "internal/config",
		"internal\\config":   "internal/config",
		"./x/../cfg":         "cfg",
		"./a/./b":            "a/b",
	} {
		if got := relDir(in); got != want {
			t.Errorf("relDir(%q) = %q, want %q", in, got, want)
		}
	}
	if got := goImportFromRel("example.com/x", "."); got != "example.com/x" {
		t.Errorf("goImportFromRel(., .) = %q, want the module path", got)
	}
	if got := displayDir("."); got != "." {
		t.Errorf("displayDir(.) = %q, want .", got)
	}
}

func TestPathHelpers(t *testing.T) {
	if got := goImportFromRel("github.com/x/y", "./internal/types"); got != "github.com/x/y/internal/types" {
		t.Errorf("got %q", got)
	}
	if got := goImportFromRel("github.com/x/y", ""); got != "github.com/x/y" {
		t.Errorf("empty rel: got %q", got)
	}
	if got := goImportFromRel("github.com/x/y", "internal/x/"); got != "github.com/x/y/internal/x" {
		t.Errorf("trailing slash: got %q", got)
	}
	if got := goImportFromRel("github.com/x/y", `internal\handler`); got != "github.com/x/y/internal/handler" {
		t.Errorf("backslash: got %q", got)
	}
	if got := fileDirRel("./svccontext/svccontext.go"); got != "svccontext" {
		t.Errorf("got %q", got)
	}
	if got := fileDirRel("main.go"); got != "" {
		t.Errorf("got %q (expected empty)", got)
	}
	if !wire.IsBodyVerb("post") || !wire.IsBodyVerb("PUT") || !wire.IsBodyVerb("PATCH") {
		t.Error("expected body verbs to be true")
	}
	if wire.IsBodyVerb("GET") || wire.IsBodyVerb("DELETE") {
		t.Error("expected non-body verbs to be false")
	}
	// A malformed @prefix contributes nothing to the route.
	svc := &ast.ServiceDecl{
		Decorators: []*ast.Decorator{
			{Name: "tags", Args: []*ast.DecoratorArg{{Value: &ast.StringLit{Value: "x"}}}},
			{Name: "prefix"}, // no args → ignored
			{Name: "prefix", Args: []*ast.DecoratorArg{{Value: &ast.IntLit{Value: 1}}}}, // wrong type → ignored
		},
	}
	m := &ast.Method{Name: "Ping"}
	if got := route.Resolve("", svc, m); got != "/ping" {
		t.Errorf("malformed @prefix must be ignored; got %q", got)
	}
	if got := route.Resolve("", nil, m); got != "/ping" {
		t.Errorf("nil service must yield bare method route; got %q", got)
	}
}
