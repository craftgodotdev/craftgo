package codegen

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/semantic"
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

// TestBuildTypeTableKeysQualifiedAcrossPackages pins the lookup
// shape that nestedValidateCall depends on: local types appear bare,
// cross-package types appear under their qualified DSL form. Without
// this the qualified-ref recursive validate call (`v.Page.Validate()`
// for `page shared.Page<ProductRef>`) is silently dropped.
func TestBuildTypeTableKeysQualifiedAcrossPackages(t *testing.T) {
	proj := &semantic.Project{
		Packages: map[string]*semantic.Package{
			"design": {
				Name:  "design",
				Types: map[string]*ast.TypeDecl{"Product": {Name: "Product"}},
			},
			"shared": {
				Name: "shared",
				Types: map[string]*ast.TypeDecl{
					"Page": {Name: "Page", TypeParams: []string{"T"}},
				},
			},
		},
	}
	tbl := BuildTypeTable(proj, "design")
	if _, ok := tbl["Product"]; !ok {
		t.Errorf("local type should be keyed bare: %v", tbl)
	}
	if _, ok := tbl["shared.Page"]; !ok {
		t.Errorf("cross-pkg generic must appear under qualified form: %v", tbl)
	}
	if _, ok := tbl["Page"]; ok {
		t.Errorf("cross-pkg type must NOT leak under bare name (would shadow local lookups): %v", tbl)
	}
}

func TestBuildTypeTableNilProjectReturnsNil(t *testing.T) {
	if BuildTypeTable(nil, "any") != nil {
		t.Error("nil project should return nil")
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
			"":       {Name: ""}, // empty default group
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
	// should be included - useful for tools that need the full map.
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

	// nil - no-op.
	set := map[string]bool{}
	walkCrossPkgImports(nil, cross, set)
	if len(set) != 0 {
		t.Errorf("nil should not contribute, got %v", set)
	}

	// Empty crossPkg - no-op even on cross-pkg ref.
	set = map[string]bool{}
	walkCrossPkgImports(mkRef("shared", "User"), nil, set)
	if len(set) != 0 {
		t.Errorf("empty crossPkg should not contribute, got %v", set)
	}

	// Single-part ref - no-op.
	set = map[string]bool{}
	walkCrossPkgImports(mkRef("User"), cross, set)
	if len(set) != 0 {
		t.Errorf("unqualified ref should not contribute, got %v", set)
	}

	// Multi-part ref - adds import.
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

// TestCollectBodyImportsMixinFileArg pins that the shared body import walk
// collects a stdlib-backed builtin (`file` → mime/multipart) that appears as
// the generic ARGUMENT of a MIXIN (`m.Box<file>` → embedded
// `m.Box[*multipart.FileHeader]`). The mixin branch must route the ref through
// collectFieldImports like the field branch does, or the generated types.go /
// errors.go references multipart without importing it.
func TestCollectBodyImportsMixinFileArg(t *testing.T) {
	cross := CrossPkg{"m": "github.com/x/internal/types/m"}
	body := []ast.TypeMember{
		&ast.Mixin{Ref: &ast.NamedTypeRef{
			Name: &ast.QualifiedIdent{Parts: []string{"m", "Box"}},
			Args: []*ast.TypeRef{{Named: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"file"}}}}},
		}},
	}
	imports := map[string]bool{}
	collectBodyImports(body, cross, imports)
	if !imports["mime/multipart"] {
		t.Errorf("a mixin with a file generic-arg must import mime/multipart; got %v", imports)
	}
	if !imports[cross["m"]] {
		t.Errorf("the mixin's own package must be imported; got %v", imports)
	}
}
