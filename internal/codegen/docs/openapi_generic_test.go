package docs

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/getkin/kin-openapi/openapi3"
)

func tRef(name string, args ...*ast.TypeRef) *ast.TypeRef {
	return &ast.TypeRef{Named: &ast.NamedTypeRef{
		Name: &ast.QualifiedIdent{Parts: []string{name}},
		Args: args,
	}}
}

func tArray(inner *ast.TypeRef) *ast.TypeRef {
	cp := *inner
	cp.Array = true
	cp.ArrayDepth = 1
	return &cp
}

func tOptional(inner *ast.TypeRef) *ast.TypeRef {
	cp := *inner
	cp.Optional = true
	return &cp
}

func tMap(key, value *ast.TypeRef) *ast.TypeRef {
	return &ast.TypeRef{Map: &ast.MapType{Key: key, Value: value}}
}

// Instance component names follow `<Decl>Of<Arg>And<Arg>` for every
// argument shape.
func TestGenericComponentName(t *testing.T) {
	cases := []struct {
		name     string
		declName string
		args     []*ast.TypeRef
		want     string
	}{
		{
			name:     "single named arg",
			declName: "Page",
			args:     []*ast.TypeRef{tRef("User")},
			want:     "PageOfUser",
		},
		{
			name:     "primitive arg",
			declName: "Page",
			args:     []*ast.TypeRef{tRef("string")},
			want:     "PageOfString",
		},
		{
			name:     "int primitive arg",
			declName: "Page",
			args:     []*ast.TypeRef{tRef("int")},
			want:     "PageOfInt",
		},
		{
			name:     "multi-param: User + Error",
			declName: "Result",
			args:     []*ast.TypeRef{tRef("User"), tRef("Error")},
			want:     "ResultOfUserAndError",
		},
		{
			name:     "nested generic arg: Page<User<Test>>",
			declName: "Page",
			args:     []*ast.TypeRef{tRef("User", tRef("Test"))},
			want:     "PageOfUserOfTest",
		},
		{
			name:     "optional arg propagates suffix",
			declName: "Page",
			args:     []*ast.TypeRef{tOptional(tRef("User"))},
			want:     "PageOfUserOrNull",
		},
		{
			name:     "array arg propagates suffix",
			declName: "Page",
			args:     []*ast.TypeRef{tArray(tRef("Order"))},
			want:     "PageOfOrderArray",
		},
		{
			name:     "map arg builds nested name",
			declName: "Envelope",
			args:     []*ast.TypeRef{tMap(tRef("string"), tRef("User"))},
			want:     "EnvelopeOfMapOfStringAndUser",
		},
		{
			name:     "deep recursion stays linear in tokens",
			declName: "Page",
			args:     []*ast.TypeRef{tRef("Result", tRef("User"), tRef("Error"))},
			want:     "PageOfResultOfUserAndError",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			decl := &ast.TypeDecl{Name: c.declName}
			got := genericComponentName(decl, c.args)
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// Registering an instance twice returns one name and leaves one pending
// entry.
func TestGenericRegistryDedup(t *testing.T) {
	r := newGenericRegistry()
	pageDecl := &ast.TypeDecl{Name: "Page"}
	args := []*ast.TypeRef{tRef("User")}
	name1 := r.register(pageDecl, args)
	name2 := r.register(pageDecl, args)
	if name1 != name2 {
		t.Errorf("register returned different names: %q vs %q", name1, name2)
	}
	if got := len(r.pending()); got != 1 {
		t.Errorf("pending after dedup = %d, want 1", got)
	}
}

// pending leaves out an instance once it is marked emitted.
func TestGenericRegistryMarkEmittedSkips(t *testing.T) {
	r := newGenericRegistry()
	r.register(&ast.TypeDecl{Name: "Page"}, []*ast.TypeRef{tRef("User")})
	pending := r.pending()
	if len(pending) != 1 {
		t.Fatalf("pending before mark = %d, want 1", len(pending))
	}
	r.markEmitted(pending[0].name)
	if len(r.pending()) != 0 {
		t.Errorf("pending after mark = %d, want 0", len(r.pending()))
	}
}

// pending returns instances in registration order.
func TestGenericRegistryOrderIsStable(t *testing.T) {
	r := newGenericRegistry()
	want := []string{
		r.register(&ast.TypeDecl{Name: "B"}, []*ast.TypeRef{tRef("X")}),
		r.register(&ast.TypeDecl{Name: "A"}, []*ast.TypeRef{tRef("X")}),
		r.register(&ast.TypeDecl{Name: "C"}, []*ast.TypeRef{tRef("X")}),
	}
	got := []string{}
	for _, inst := range r.pending() {
		got = append(got, inst.name)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("pending order:\n  got:  %v\n  want: %v", got, want)
	}
}

// Two distinct instances named alike (`Page<IntArray>`, `Page<int[]>`) are
// rejected; distinct names are not.
func TestGenericInstanceNameCollisionRejected(t *testing.T) {
	mk := func(respFields string) (*openapi3.T, error) {
		root, files := projectFiles(t, map[string]string{
			"app/app.craftgo": `package app
type Page<T> { items T[] }
type IntArray { whatever int }
type Req { id string }
type Resp { ` + respFields + ` }
service S { post G /g { request Req  response Resp } }`,
		})
		proj, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root})
		if len(diags) > 0 {
			t.Fatalf("semantic: %v", diags)
		}
		return buildProjectDocument(proj, &config.Config{})
	}
	if _, err := mk("real Page<IntArray>  prim Page<int[]>"); err == nil || !strings.Contains(err.Error(), "structurally distinct generic") {
		t.Errorf("expected generic-instance collision error, got: %v", err)
	}
	if _, err := mk("a Page<int>  b Page<string>"); err != nil {
		t.Errorf("distinct generic instances wrongly rejected: %v", err)
	}
}
