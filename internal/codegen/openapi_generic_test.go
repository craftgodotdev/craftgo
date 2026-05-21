package codegen

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// helper builders for TypeRef AST so the table-driven tests below stay
// readable. The tests would otherwise drown in 4-line literal struct
// initialisers and the actual assertion intent would be lost.

func tRef(name string, args ...*ast.TypeRef) *ast.TypeRef {
	return &ast.TypeRef{Named: &ast.NamedTypeRef{
		Name: &ast.QualifiedIdent{Parts: []string{name}},
		Args: args,
	}}
}

func tQualifiedRef(pkg, name string, args ...*ast.TypeRef) *ast.TypeRef {
	return &ast.TypeRef{Named: &ast.NamedTypeRef{
		Name: &ast.QualifiedIdent{Parts: []string{pkg, name}},
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

// TestGenericComponentName exercises the naming function across every
// shape the registry produces: single-param, multi-param, primitive
// arg, cross-pkg arg, nested generic arg, optional arg, array arg,
// map arg. The expected strings are the wire-level contract that
// downstream client codegen (TypeScript / Python / etc.) reads off
// the OpenAPI spec; renaming any of them is a breaking change for
// every consumer.
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
			name:     "cross-pkg arg",
			declName: "Page",
			args:     []*ast.TypeRef{tQualifiedRef("users", "User")},
			want:     "PageOfUsersUser",
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

// TestGenericRegistryDedup pins the dedup contract: registering the
// same (decl, args) tuple twice returns the same component name AND
// does NOT inflate the pending list. Without this guarantee, the
// emitter would walk the same body twice and write duplicate schemas
// to `components.schemas`.
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

// TestGenericRegistryMarkEmittedSkips checks the emission loop's exit
// condition: once a name is marked emitted, `pending` must not return
// it again, otherwise the emit loop would never terminate.
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

// TestPascalQualified covers the cross-pkg name fragment generation.
// Single-segment names pass through with first-rune uppercased;
// qualified `pkg.Name` names join segments PascalCase-style so the
// final synthetic component name is collision-safe inside the flat
// OpenAPI `components.schemas` namespace.
func TestPascalQualified(t *testing.T) {
	cases := map[string]string{
		"":            "",
		"x":           "X",
		"User":        "User",
		"users.User":  "UsersUser",
		"a.b.c":       "ABC",
		"my_pkg.Foo":  "My_pkgFoo", // underscores in segments stay (PascalCase per-segment only)
		"my-pkg.Foo":  "My-pkgFoo",
	}
	for in, want := range cases {
		got := pascalQualified(in)
		if got != want {
			t.Errorf("pascalQualified(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestIsPrimitiveName ensures the primitive set matches the DSL's
// builtin types verbatim. A missing entry would let a user-declared
// type silently shadow a primitive name in the synthetic component
// naming, leading to surprising `$ref`s in client code.
func TestIsPrimitiveName(t *testing.T) {
	prim := []string{"string", "bool", "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64", "bytes", "any", "file"}
	for _, p := range prim {
		if !isPrimitiveName(p) {
			t.Errorf("%q should be primitive", p)
		}
	}
	for _, p := range []string{"User", "string1", "STRING", "Float64", ""} {
		if isPrimitiveName(p) {
			t.Errorf("%q should NOT be primitive", p)
		}
	}
}

// TestGenericRegistryOrderIsStable pins the iteration order to
// registration order. OpenAPI YAML serialisation depends on a stable
// component ordering for deterministic builds - any drift in pending()
// ordering would surface as noisy diffs after every regen.
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
