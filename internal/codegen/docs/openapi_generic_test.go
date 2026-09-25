package docs

import (
	"encoding/json"
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
			name:     "array of maps leads with ArrayOf",
			declName: "Envelope",
			args:     []*ast.TypeRef{tArray(tMap(tRef("string"), tRef("User")))},
			want:     "EnvelopeOfArrayOfMapOfStringAndUser",
		},
		{
			name:     "2-D array of maps",
			declName: "Envelope",
			args:     []*ast.TypeRef{{Map: &ast.MapType{Key: tRef("string"), Value: tRef("User")}, Array: true, ArrayDepth: 2}},
			want:     "EnvelopeOfArrayOfArrayOfMapOfStringAndUser",
		},
		{
			name:     "map of arrays ends with Array",
			declName: "Envelope",
			args:     []*ast.TypeRef{tMap(tRef("string"), tArray(tRef("User")))},
			want:     "EnvelopeOfMapOfStringAndUserArray",
		},
		{
			name:     "optional map value ends with OrNull",
			declName: "Envelope",
			args:     []*ast.TypeRef{tMap(tRef("string"), tOptional(tRef("User")))},
			want:     "EnvelopeOfMapOfStringAndUserOrNull",
		},
		{
			name:     "optional array map value",
			declName: "Envelope",
			args:     []*ast.TypeRef{tMap(tRef("string"), tOptional(tArray(tRef("User"))))},
			want:     "EnvelopeOfMapOfStringAndUserArrayOrNull",
		},
		{
			name:     "optional map map value leads with NullOr",
			declName: "Envelope",
			args:     []*ast.TypeRef{tMap(tRef("string"), tOptional(tMap(tRef("string"), tRef("User"))))},
			want:     "EnvelopeOfMapOfStringAndNullOrMapOfStringAndUser",
		},
		{
			name:     "nested instance over an array ends with Array",
			declName: "Page",
			args:     []*ast.TypeRef{tRef("Box", tArray(tRef("Item")))},
			want:     "PageOfBoxOfItemArray",
		},
		{
			name:     "array of a nested instance leads with ArrayOf",
			declName: "Page",
			args:     []*ast.TypeRef{tArray(tRef("Box", tRef("Item")))},
			want:     "PageOfArrayOfBoxOfItem",
		},
		{
			name:     "a nested pair ends its leaves with Array",
			declName: "Page",
			args:     []*ast.TypeRef{tRef("Pair", tRef("string"), tArray(tRef("Item")))},
			want:     "PageOfPairOfStringAndItemArray",
		},
		{
			name:     "every leaf argument keeps its suffix",
			declName: "Pair",
			args:     []*ast.TypeRef{tArray(tRef("Item")), tArray(tRef("Item"))},
			want:     "PairOfItemArrayAndItemArray",
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

// A generic request or response split by a header binds its arguments to its
// own fields and headers alone: the `t` the non-generic Meta brings stays the
// scalar T although both generics spell their parameter T.
func TestSplitGenericBodiesSubstituteOnTheirOwnLevel(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"a/a.craftgo": `package a
scalar T string
enum Prio { low  high }
type Meta { t T? }
type Paged<T> {
	Meta
	count T @header("X-Count")
	data  T
}
type Put<T> {
	Meta
	trace string @header("X-Trace")
	data  T
}
type Item { id string }
service S {
	get L /l { response Paged<Prio> }
	post P /p { request Put<int>  response Item }
}`,
	}, &config.Config{})
	nullableT := `{"anyOf":[{"$ref":"#/components/schemas/T"},{"type":"null"}]}`
	for name, c := range map[string]struct {
		got  *openapi3.SchemaRef
		want string
	}{
		"LRespBody.t":    {doc.Components.Schemas["LRespBody"].Value.Properties["t"], nullableT},
		"LRespBody.data": {doc.Components.Schemas["LRespBody"].Value.Properties["data"], `{"$ref":"#/components/schemas/Prio"}`},
		"PReqBody.t":     {doc.Components.Schemas["PReqBody"].Value.Properties["t"], nullableT},
		"PReqBody.data":  {doc.Components.Schemas["PReqBody"].Value.Properties["data"], `{"type":"integer"}`},
		"X-Count":        {doc.Paths.Find("/l").Get.Responses.Status(200).Value.Headers["X-Count"].Value.Schema, `{"$ref":"#/components/schemas/Prio"}`},
	} {
		got, err := json.Marshal(c.got)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != c.want {
			t.Errorf("%s = %s, want %s", name, got, c.want)
		}
	}
}

// A type parameter spelled like a declaration is documented as its argument.
func TestGenericTypeParamShadowsDeclaration(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"a/a.craftgo": `package a
scalar Blob bytes
enum Color { Red  Green }
type Box<Blob, Color> { v Blob?  c Color }
type Host { b Box<int, string> }
service S { get L /l { response Host } }`,
	}, &config.Config{})
	box := doc.Components.Schemas["BoxOfIntAndString"].Value
	for field, want := range map[string]string{"v": `{"type":["integer","null"]}`, "c": `{"type":"string"}`} {
		got, err := json.Marshal(box.Properties[field])
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s = %s, want %s", field, got, want)
		}
	}
}

// A response header typed by a type parameter is documented as the
// instance's argument.
func TestGenericResponseHeaderTakesItsArgument(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"a/a.craftgo": `package a
type Tallied<T> { tally T @header("X-Tally")  items T[] }
service S { get L /l { response Tallied<int> } }`,
	}, &config.Config{})
	got, err := json.Marshal(doc.Paths.Find("/l").Get.Responses.Status(200).Value.Headers["X-Tally"].Value.Schema)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"type":"integer"}` {
		t.Errorf("X-Tally = %s, want an integer", got)
	}
}

// A declared type named like another instance's argument, `IntArray` or
// `String`, is rejected; instances that differ in shape never share a name.
func TestGenericInstanceNameCollisionRejected(t *testing.T) {
	mk := func(respFields string) (*openapi3.T, error) {
		root, files := projectFiles(t, map[string]string{
			"app/app.craftgo": `package app
type Page<T> { items T[] }
type Box<T> { v T }
type Pair<A, B> { a A  b B }
type IntArray { whatever int }
type String { whatever int }
type Item { id string }
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
	for fields, clash := range map[string]string{
		"real Page<IntArray>  prim Page<int[]>": "PageOfIntArray",
		"real Page<String>  prim Page<string>":  "PageOfString",
	} {
		_, err := mk(fields)
		if err == nil || !strings.Contains(err.Error(), "structurally distinct generic") || !strings.Contains(err.Error(), clash) {
			t.Errorf("%s: expected a generic-instance collision on %s, got: %v", fields, clash, err)
			continue
		}
		if strings.Contains(err.Error(), "struct of") {
			t.Errorf("%s: hint names a struct: %v", fields, err)
		}
	}
	for _, fields := range []string{
		"a Page<int>  b Page<string>",
		"a Page<map<string, int>>  b Page<map<string, int>[]>",
		"a Page<map<string, Item[]>>  b Page<map<string, Item>[]>",
		"a Page<map<string, Item[]>[]>  b Page<map<string, Item[][]>>  c Page<map<string, Item>[][]>",
		"a Page<map<string, map<string, int>[]>>  b Page<map<string, map<string, int[]>>>",
		"a Page<map<string, map<string, int>?>>  b Page<map<string, map<string, int?>>>",
		"a Page<Box<Item[]>>  b Page<Box<Item>[]>",
		"a Page<Pair<string, Item[]>>  b Page<Pair<string, Item>[]>",
		"a Pair<map<string, Item[]>, int>  b Pair<map<string, Item>[], int>",
	} {
		if _, err := mk(fields); err != nil {
			t.Errorf("distinct generic instances %s wrongly rejected: %v", fields, err)
		}
	}
}
