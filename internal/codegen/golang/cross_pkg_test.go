package golang

import (
	"slices"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
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

// A set with no home, a file of the DSL package's own types, names that package's types bare and
// imports the package of each qualified ref, through map values, generic arguments and a mixin's
// builtin argument too.
func TestHomelessImportSet(t *testing.T) {
	const shared = "github.com/x/internal/types/shared"
	named := func(parts ...string) *ast.TypeRef {
		return &ast.TypeRef{Named: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: parts}}}
	}
	cases := []struct {
		name  string
		ref   *ast.TypeRef
		spelt string
		want  []string
	}{
		{"bare", named("User"), "User", nil},
		{"qualified", named("shared", "User"), "shared.User", []string{shared}},
		{"map value", &ast.TypeRef{Map: &ast.MapType{Key: named("string"), Value: named("shared", "User")}}, "map[string]shared.User", []string{shared}},
		{"generic argument", &ast.TypeRef{Named: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"Page"}}, Args: []*ast.TypeRef{named("shared", "User")}}}, "Page[shared.User]", []string{shared}},
		{"builtin argument", &ast.TypeRef{Named: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"shared", "Box"}}, Args: []*ast.TypeRef{named("file")}}}, "shared.Box[*multipart.FileHeader]", []string{shared, "mime/multipart"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			set := newImportSet(&projectResolver{CrossPkg: crossPkg{"shared": shared}}, goImport{}, typesNames)
			if got := set.goType(c.ref); got != c.spelt {
				t.Errorf("spelt %q, want %q", got, c.spelt)
			}
			var got []string
			for _, imp := range set.imports() {
				got = append(got, imp.Path)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("imports %v, want %v", got, c.want)
			}
		})
	}
}
