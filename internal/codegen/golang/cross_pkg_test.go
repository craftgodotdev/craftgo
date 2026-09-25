package golang

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// buildCrossPkg maps each other package to its Go import path and leaves out the current one.
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
	cross := buildCrossPkg(proj, cfg, "design")
	if got := cross["shared"]; got != "github.com/test/multi/internal/types/shared" {
		t.Errorf("expected mapped Go import, got %q", got)
	}
	if _, ok := cross["design"]; ok {
		t.Error("self-package should not appear in CrossPkg")
	}
}

func TestBuildCrossPkgReturnsNilOnNilInputs(t *testing.T) {
	if buildCrossPkg(nil, &config.Config{}, "") != nil {
		t.Error("nil project should return nil")
	}
	if buildCrossPkg(&semantic.Project{}, nil, "") != nil {
		t.Error("nil cfg should return nil")
	}
}

func TestBuildCrossPkgSkipsCurrent(t *testing.T) {
	cfg := &config.Config{Package: "x", Output: config.Output{Types: "./types"}}
	proj := &semantic.Project{
		Packages: map[string]*semantic.Package{
			"design": {Name: "design"},
			"shared": {Name: "shared"},
		},
	}
	cross := buildCrossPkg(proj, cfg, "design")
	if _, ok := cross["design"]; ok {
		t.Error("current package should not appear")
	}
	if cross["shared"] != "x/types/shared" {
		t.Errorf("shared should resolve, got %q", cross["shared"])
	}
}

// With no current package, buildCrossPkg maps every package.
func TestBuildCrossPkgEmptyCurrentReturnsAll(t *testing.T) {
	cfg := &config.Config{Package: "x", Output: config.Output{Types: "./types"}}
	proj := &semantic.Project{
		Packages: map[string]*semantic.Package{
			"a": {Name: "a"},
			"b": {Name: "b"},
		},
	}
	cross := buildCrossPkg(proj, cfg, "")
	if len(cross) != 2 {
		t.Errorf("expected 2 entries, got %d: %v", len(cross), cross)
	}
}

// importsInto collects imports only for qualified refs, in map values and generic args too.
func TestImportsInto(t *testing.T) {
	cross := crossPkg{"shared": "github.com/x/internal/types/shared"}

	mkRef := func(parts ...string) *ast.TypeRef {
		return &ast.TypeRef{Named: &ast.NamedTypeRef{
			Name: &ast.QualifiedIdent{Pos: lexer.Position{}, Parts: parts},
		}}
	}

	set := map[string]bool{}
	(*ast.TypeRef)(nil).WalkNamedRefs(cross.importsInto(set))
	if len(set) != 0 {
		t.Errorf("nil should not contribute, got %v", set)
	}

	set = map[string]bool{}
	mkRef("shared", "User").WalkNamedRefs(crossPkg(nil).importsInto(set))
	if len(set) != 0 {
		t.Errorf("empty crossPkg should not contribute, got %v", set)
	}

	set = map[string]bool{}
	mkRef("User").WalkNamedRefs(cross.importsInto(set))
	if len(set) != 0 {
		t.Errorf("unqualified ref should not contribute, got %v", set)
	}

	set = map[string]bool{}
	mkRef("shared", "User").WalkNamedRefs(cross.importsInto(set))
	if !set[cross["shared"]] {
		t.Errorf("multi-part ref should add import, got %v", set)
	}

	set = map[string]bool{}
	(&ast.TypeRef{Map: &ast.MapType{
		Key:   mkRef("string"),
		Value: mkRef("shared", "User"),
	}}).WalkNamedRefs(cross.importsInto(set))
	if !set[cross["shared"]] {
		t.Errorf("map value should propagate, got %v", set)
	}

	set = map[string]bool{}
	(&ast.TypeRef{Named: &ast.NamedTypeRef{
		Name: &ast.QualifiedIdent{Parts: []string{"Page"}},
		Args: []*ast.TypeRef{mkRef("shared", "User")},
	}}).WalkNamedRefs(cross.importsInto(set))
	if !set[cross["shared"]] {
		t.Errorf("generic arg should propagate, got %v", set)
	}
}

// crossPkgImportFor returns "" for a nil map or ref, a bare name and an unknown alias.
func TestCrossPkgImportForGuards(t *testing.T) {
	if got := crossPkgImportFor(&ast.NamedTypeRef{}, nil); got != "" {
		t.Error("nil map should return empty")
	}
	if got := crossPkgImportFor(nil, crossPkg{"a": "b"}); got != "" {
		t.Error("nil ref should return empty")
	}
	if got := crossPkgImportFor(&ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"User"}}}, crossPkg{"shared": "x"}); got != "" {
		t.Error("single-part should return empty")
	}
	if got := crossPkgImportFor(&ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"unknown", "T"}}}, crossPkg{"shared": "x"}); got != "" {
		t.Error("unknown alias should return empty")
	}
}

// A mixin's generic argument contributes its imports, e.g. file → mime/multipart.
func TestCollectBodyImportsMixinFileArg(t *testing.T) {
	cross := crossPkg{"m": "github.com/x/internal/types/m"}
	body := []ast.TypeMember{
		&ast.Mixin{Ref: &ast.NamedTypeRef{
			Name: &ast.QualifiedIdent{Parts: []string{"m", "Box"}},
			Args: []*ast.TypeRef{{Named: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"file"}}}}},
		}},
	}
	imports := map[string]bool{}
	collectBodyImports(body, &semantic.Package{}, &projectResolver{CrossPkg: cross}, imports)
	if !imports["mime/multipart"] {
		t.Errorf("a mixin with a file generic-arg must import mime/multipart; got %v", imports)
	}
	if !imports[cross["m"]] {
		t.Errorf("the mixin's own package must be imported; got %v", imports)
	}
}
