package semantic

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// A field promoted through another package's mixin is spelled as the
// resolver's package spells it, a generic mixin's type argument included.
func TestFlattenWithNamesRequalifiesPromotedFields(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/s.craftgo": `package shared
type IdHolder<T> { id T }
scalar Code string
type Local { x string }
type Base {
    IdHolder<Code>
    local Local
    at    datetime
}`,
		"app/a.craftgo": `package app
type Req { shared.Base  own string }`,
	})
	proj, diags := AnalyzeProject(files, Options{DesignRoot: root})
	expectNoDiags(t, diags)
	r := NewResolver(proj, "app")
	got := map[string]string{}
	for _, ff := range FlattenWithNames(proj.Packages["app"].Types["Req"], "", proj.Packages["app"], r, nil) {
		got[ff.Field.Name] = ff.Field.Type.Named.Name.String() + " from " + ff.Home
	}
	want := map[string]string{
		"id":    "shared.Code from shared",
		"local": "shared.Local from shared",
		"at":    "datetime from shared",
		"own":   "string from app",
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("field %s = %q, want %q", name, got[name], w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("fields = %v, want %v", got, want)
	}
}

// A type parameter keeps its bare name even when the mixin's package
// declares a type of that name.
func TestFlattenKeepsShadowingTypeParameter(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/s.craftgo": `package shared
type T { x string }
type Holder<T> { v T }
type Base { Holder<string> }`,
		"app/a.craftgo": `package app
type Req { shared.Base }`,
	})
	proj, diags := AnalyzeProject(files, Options{DesignRoot: root})
	expectNoDiags(t, diags)
	fields := FlattenFields(proj.Packages["app"].Types["Req"], proj.Packages["app"], NewResolver(proj, "app"))
	if len(fields) != 1 || fields[0].Type.Named.Name.String() != "string" {
		var names []string
		for _, f := range fields {
			names = append(names, f.Name+" "+f.Type.Named.Name.String())
		}
		t.Errorf("fields = %v, want [v string]", names)
	}
}

// A nil resolver expands no mixin.
func TestFlattenWithNilResolverKeepsOwnFields(t *testing.T) {
	td := &ast.TypeDecl{Body: []ast.TypeMember{
		&ast.Mixin{Ref: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"Base"}}}},
		&ast.Field{Name: "own", Type: ast.Named("string")},
	}}
	fields := FlattenFields(td, nil, nil)
	if len(fields) != 1 || fields[0].Name != "own" {
		t.Errorf("fields = %v, want [own]", fields)
	}
}
