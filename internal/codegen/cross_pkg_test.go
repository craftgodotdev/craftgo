package codegen

import (
	"testing"

	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/config"
	"github.com/dropship-dev/craftgo/internal/lexer"
	"github.com/dropship-dev/craftgo/internal/semantic"
)

// TestBuildCrossPkgResolves verifies the happy path: a project with
// two declared packages produces a CrossPkg pointing the non-current
// one at the right Go import path.
func TestBuildCrossPkgResolves(t *testing.T) {
	cfg := &config.Config{
		Package: "github.com/test/multi",
		Output:  config.Output{Types: "./internal/types"},
	}
	proj := &semantic.Project{
		Packages: map[string]*semantic.Package{
			"design": {Name: "design"},
			"shared": {Name: "shared"},
		},
	}
	cross := BuildCrossPkg(proj, cfg, "design")
	if got := cross["shared"]; got != "github.com/test/multi/internal/types/shared" {
		t.Errorf("expected mapped Go import, got %q", got)
	}
	// Self-package excluded.
	if _, ok := cross["design"]; ok {
		t.Error("self-package should not appear in CrossPkg")
	}
}

func TestBuildCrossPkgReturnsNilOnNilInputs(t *testing.T) {
	if BuildCrossPkg(nil, &config.Config{}, "") != nil {
		t.Error("nil project should return nil")
	}
	if BuildCrossPkg(&semantic.Project{}, nil, "") != nil {
		t.Error("nil cfg should return nil")
	}
}

func TestBuildCrossPkgSkipsEmptyAndCurrent(t *testing.T) {
	cfg := &config.Config{Package: "x", Output: config.Output{Types: "./types"}}
	proj := &semantic.Project{
		Packages: map[string]*semantic.Package{
			"":       {Name: ""},        // empty default group
			"design": {Name: "design"},
			"shared": {Name: "shared"},
		},
	}
	cross := BuildCrossPkg(proj, cfg, "design")
	if _, ok := cross[""]; ok {
		t.Error("empty key should not appear")
	}
	if _, ok := cross["design"]; ok {
		t.Error("current package should not appear")
	}
	if cross["shared"] != "x/types/shared" {
		t.Errorf("shared should resolve, got %q", cross["shared"])
	}
}

func TestBuildCrossPkgEmptyCurrentReturnsAll(t *testing.T) {
	// When called without a current package, every non-empty package
	// should be included — useful for tools that need the full map.
	cfg := &config.Config{Package: "x", Output: config.Output{Types: "./types"}}
	proj := &semantic.Project{
		Packages: map[string]*semantic.Package{
			"a": {Name: "a"},
			"b": {Name: "b"},
		},
	}
	cross := BuildCrossPkg(proj, cfg, "")
	if len(cross) != 2 {
		t.Errorf("expected 2 entries, got %d: %v", len(cross), cross)
	}
}

// TestWalkCrossPkgImports drives the walker through every shape:
// nil, map, named with multi-part, generic args.
func TestWalkCrossPkgImports(t *testing.T) {
	cross := CrossPkg{"shared": "github.com/x/internal/types/shared"}

	mkRef := func(parts ...string) *ast.TypeRef {
		return &ast.TypeRef{Named: &ast.NamedTypeRef{
			Name: &ast.QualifiedIdent{Pos: lexer.Position{}, Parts: parts},
		}}
	}

	// nil — no-op.
	set := map[string]bool{}
	walkCrossPkgImports(nil, cross, set)
	if len(set) != 0 {
		t.Errorf("nil should not contribute, got %v", set)
	}

	// Empty crossPkg — no-op even on cross-pkg ref.
	set = map[string]bool{}
	walkCrossPkgImports(mkRef("shared", "User"), nil, set)
	if len(set) != 0 {
		t.Errorf("empty crossPkg should not contribute, got %v", set)
	}

	// Single-part ref — no-op.
	set = map[string]bool{}
	walkCrossPkgImports(mkRef("User"), cross, set)
	if len(set) != 0 {
		t.Errorf("unqualified ref should not contribute, got %v", set)
	}

	// Multi-part ref — adds import.
	set = map[string]bool{}
	walkCrossPkgImports(mkRef("shared", "User"), cross, set)
	if !set[cross["shared"]] {
		t.Errorf("multi-part ref should add import, got %v", set)
	}

	// Map with cross-pkg value.
	set = map[string]bool{}
	walkCrossPkgImports(&ast.TypeRef{Map: &ast.MapType{
		Key:   mkRef("string"),
		Value: mkRef("shared", "User"),
	}}, cross, set)
	if !set[cross["shared"]] {
		t.Errorf("map value should propagate, got %v", set)
	}

	// Generic arg with cross-pkg ref.
	set = map[string]bool{}
	walkCrossPkgImports(&ast.TypeRef{Named: &ast.NamedTypeRef{
		Name: &ast.QualifiedIdent{Parts: []string{"Page"}},
		Args: []*ast.TypeRef{mkRef("shared", "User")},
	}}, cross, set)
	if !set[cross["shared"]] {
		t.Errorf("generic arg should propagate, got %v", set)
	}
}

func TestCrossPkgImportForGuards(t *testing.T) {
	// Empty map → nothing.
	if got := crossPkgImportFor(&ast.NamedTypeRef{}, nil); got != "" {
		t.Error("nil map should return empty")
	}
	// Nil ref → nothing.
	if got := crossPkgImportFor(nil, CrossPkg{"a": "b"}); got != "" {
		t.Error("nil ref should return empty")
	}
	// Single-part name → nothing.
	if got := crossPkgImportFor(&ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"User"}}}, CrossPkg{"shared": "x"}); got != "" {
		t.Error("single-part should return empty")
	}
	// Unknown alias → nothing.
	if got := crossPkgImportFor(&ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"unknown", "T"}}}, CrossPkg{"shared": "x"}); got != "" {
		t.Error("unknown alias should return empty")
	}
}
