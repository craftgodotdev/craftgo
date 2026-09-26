package semantic

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
)

// A field promoted through another package's mixin is spelled as the
// resolver's package spells it, a generic mixin's type argument included.
func TestFlattenFieldsRequalifiesPromotedFields(t *testing.T) {
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
	for _, ff := range FlattenFields(proj.Packages["app"].Types["Req"], "", r, nil) {
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
	fields := FlattenFields(proj.Packages["app"].Types["Req"], "", NewResolver(proj, "app"), nil)
	if len(fields) != 1 || fields[0].Field.Type.Named.Name.String() != "string" {
		var names []string
		for _, ff := range fields {
			names = append(names, ff.Field.Name+" "+ff.Field.Type.Named.Name.String())
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
	fields := FlattenFields(td, "", nil, nil)
	if len(fields) != 1 || fields[0].Field.Name != "own" {
		t.Errorf("fields = %v, want [own]", fields)
	}
}

// A generic mixin's arguments bind its own fields and the arguments of the
// mixins it embeds, never the fields a non-generic mixin brings: Meta's `t`
// names the declared T although Page's parameter is spelled T too.
func TestFlattenSubstitutesPerLevel(t *testing.T) {
	pkg := mustClean(t, `package app
scalar T string
enum Kind { a  b }
type Meta { t T? }
type Inner<U> { u U }
type Page<T> { Meta  Inner<T>  k T? }
type Req { Page<Kind> }`)
	got := map[string]string{}
	for _, ff := range FlattenFields(pkg.Types["Req"], "", PackageResolver(pkg), nil) {
		got[ff.Field.Name] = ff.Field.Type.String()
	}
	want := map[string]string{"t": "T?", "u": "Kind", "k": "Kind?"}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("field %s = %q, want %q", name, got[name], w)
		}
	}
}

// goNames names each field of a level as Go does, for the tests.
func goNames(members []ast.TypeMember) []string {
	var out []string
	for _, f := range ast.Fields(members) {
		out = append(out, idents.GoFieldName(f.Name))
	}
	return out
}

// A promoted field whose Go name an embedded mixin at its depth or above
// also carries is named by its embed path: Page's `page` is Page.Page from
// ListReq and Wrap.Page.Page from Deep, and C's `pager`, level with the
// Pager that A embeds, is C.Pager from Both.
func TestFlattenNamesShadowedPromotedFieldByItsPath(t *testing.T) {
	pkg := mustClean(t, `package app
type Page { page int  size int? }
type ListReq { Page  q string? }
type Wrap { Page }
type Deep { Wrap }
type Pager { n int }
type A { Pager }
type C { pager int? }
type Both { A  C }`)
	r := PackageResolver(pkg)
	for typ, want := range map[string]string{
		"ListReq": "page=Page.Page size=Size q=Q",
		"Deep":    "page=Wrap.Page.Page size=Size",
		"Both":    "n=N pager=C.Pager",
	} {
		var got []string
		for _, ff := range FlattenFields(pkg.Types[typ], "", r, goNames) {
			got = append(got, ff.Field.Name+"="+ff.Name)
		}
		if strings.Join(got, " ") != want {
			t.Errorf("%s: names = %q, want %q", typ, strings.Join(got, " "), want)
		}
	}
}
