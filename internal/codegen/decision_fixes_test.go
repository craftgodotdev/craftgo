package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// A scalar over the nilable `bytes` primitive lowers to the bare named
// slice, so an optional / @nullable field of it must NOT carry a
// redundant pointer — it renders like a raw `bytes` field, and its
// validator nil-guards before calling the scalar's own Validate().
func TestScalarOverBytesNullableRendersWithoutPointer(t *testing.T) {
	root, files := projectFiles(t, map[string]string{
		"m/m.craftgo": `package m
scalar Blob bytes @minLength(4)
type Doc {
  reqBlob Blob
  nulBlob Blob @nullable
  optBlob Blob?
}`,
	})
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("semantic: %v", diags)
	}
	dir := t.TempDir()
	mPkg := proj.Packages["m"]
	if err := GenerateTypesPackage(mPkg, dir, CrossPkg{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := GenerateValidatorsAll(mPkg, dir, CrossPkg{}, BuildScalarTable(proj, "m"), BuildTypeTable(proj, "m"), BuildEnumTable(proj, "m")); err != nil {
		t.Fatal(err)
	}
	types, _ := os.ReadFile(filepath.Join(dir, "m", "types.go"))
	val, _ := os.ReadFile(filepath.Join(dir, "m", "validate.go"))
	mustParseGo(t, string(types))
	mustParseGo(t, string(val))
	ts := string(types)
	if strings.Contains(ts, "*Blob") {
		t.Errorf("scalar-over-bytes field rendered with a redundant pointer (*Blob):\n%s", ts)
	}
	mustContainAll(t, ts,
		"NulBlob Blob `json:\"nulBlob\"`",
		"OptBlob Blob `json:\"optBlob,omitempty\"`",
	)
	// The nullable/optional scalar's Validate() stays nil-guarded so a
	// null / absent value skips the scalar's own constraint.
	mustContainAll(t, string(val),
		"if v.NulBlob != nil {",
		"if v.OptBlob != nil {",
	)
}

// A scalar over a nilable primitive can no longer participate in a
// cross-field group: it lowers to a non-pointer nilable slice, so its
// runtime presence is emptiness (not a clean `!= nil`), which disagrees
// with the group's OpenAPI present-and-non-null — reject like raw bytes.
func TestScalarOverBytesRejectedInCrossFieldGroup(t *testing.T) {
	root, files := projectFiles(t, map[string]string{
		"m/m.craftgo": `package m
scalar Blob bytes
@requiresOneOf(a, b)
type Pick {
  a Blob?
  b string?
}`,
	})
	_, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root})
	if len(diags) == 0 {
		t.Fatal("expected a diagnostic rejecting the scalar-over-bytes cross-field member")
	}
	found := false
	for _, d := range diags {
		if strings.Contains(d.Msg, "present/absent") || strings.Contains(d.Msg, "always treated as present") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a present/absent rejection, got: %v", diags)
	}
}

// A scalar-over-VALUE primitive (int) stays pointer-backed when
// optional, so it remains a clean cross-field member — the reject above
// must not over-fire.
func TestScalarOverValueCrossFieldClean(t *testing.T) {
	root, files := projectFiles(t, map[string]string{
		"m/m.craftgo": `package m
scalar Cents int
@requiresOneOf(a, b)
type Pick {
  a Cents?
  b string?
}`,
	})
	_, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root})
	for _, d := range diags {
		if strings.Contains(d.Msg, "present/absent") {
			t.Fatalf("scalar-over-int cross-field member wrongly rejected: %v", d)
		}
	}
}

// @doc / @example on a field whose type is a named ref ($ref) is carried
// onto an allOf wrapper instead of being dropped — a bare $ref can't hold
// sibling keywords portably.
func TestNamedRefDocExampleWrappedInAllOf(t *testing.T) {
	root, files := projectFiles(t, map[string]string{
		"m/m.craftgo": `package m
type Inner { x int }
type Outer {
  child Inner @doc("the inner child") @example("ignored-by-object")
}`,
	})
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("semantic: %v", diags)
	}
	merged := mergeProjectForOpenAPI(proj)
	doc, err := buildOpenAPIDoc(merged, &config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	outer := doc.Components.Schemas["Outer"]
	if outer == nil || outer.Value == nil {
		t.Fatal("no Outer schema")
	}
	child := outer.Value.Properties["child"]
	if child == nil || child.Value == nil {
		t.Fatal("no child property")
	}
	if child.Ref != "" {
		t.Fatalf("child stayed a bare $ref; @doc dropped: %q", child.Ref)
	}
	if len(child.Value.AllOf) != 1 || child.Value.AllOf[0].Ref == "" {
		t.Fatalf("child not wrapped in allOf:[{$ref}]: %+v", child.Value)
	}
	if child.Value.Description != "the inner child" {
		t.Fatalf("@doc not carried onto the wrapper: %q", child.Value.Description)
	}
}

// When two packages declare an error of the same name, the OpenAPI merge
// renames both; the method's @errors decorator must follow the rename so
// its error response is not silently dropped from the spec.
func TestCrossPkgErrorNameCollisionFollowsRename(t *testing.T) {
	root, files := projectFiles(t, map[string]string{
		"a/a.craftgo": `package a
error Conflict Dup { reason string }
service ASvc {
  @errors(Dup)
  post Make /a { request Mk }
}
type Mk { name string }`,
		"b/b.craftgo": `package b
import "a"
error Conflict Dup { reason string }
service BSvc {
  @errors(Dup)
  post MakeB /b { request MkB }
  @errors(a.Dup)
  post MakeC /c { request MkB }
}
type MkB { name string }`,
	})
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("semantic: %v", diags)
	}
	merged := mergeProjectForOpenAPI(proj)
	doc, err := buildOpenAPIDoc(merged, &config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	check := func(path, wantSuffix string) {
		item := doc.Paths.Find(path)
		if item == nil || item.Post == nil {
			t.Fatalf("no POST %s", path)
		}
		r409 := item.Post.Responses.Status(409)
		if r409 == nil || r409.Value == nil {
			t.Fatalf("%s: 409 @errors response dropped", path)
		}
		mt := r409.Value.Content.Get("application/json")
		if mt == nil || mt.Schema == nil || !strings.HasSuffix(mt.Schema.Ref, wantSuffix) {
			got := ""
			if mt != nil && mt.Schema != nil {
				got = mt.Schema.Ref
			}
			t.Fatalf("%s: 409 ref=%q, want suffix %q", path, got, wantSuffix)
		}
	}
	check("/a", "ADupErr") // a's own Dup, renamed
	check("/b", "BDupErr") // b's own Dup (bare), renamed
	check("/c", "ADupErr") // b -> a.Dup (qualified), follows a's rename
}

// genDoc analyzes src into a merged OpenAPI doc for assertions.
func genDoc(t *testing.T, src map[string]string, cfg *config.Config) *openapi3.T {
	t.Helper()
	root, files := projectFiles(t, src)
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("semantic: %v", diags)
	}
	doc, err := buildOpenAPIDoc(mergeProjectForOpenAPI(proj), cfg)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// @errors([...]) and @tags([...]) array-shortcut forms must contribute the
// same responses / tags as the variadic form (they were silently dropped).
func TestArrayShortcutErrorsAndTags(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"s/s.craftgo": `package s
error NotFound Missing { resource string }
type Out { ok bool }
service S {
  @errors([Missing])
  @tags([alpha, beta])
  get Arr /arr { response Out }
}`,
	}, &config.Config{})
	op := doc.Paths.Find("/arr").Get
	if op.Responses.Status(404) == nil {
		t.Error("@errors([Missing]) dropped the 404 response")
	}
	if len(op.Tags) != 2 || op.Tags[0] != "alpha" || op.Tags[1] != "beta" {
		t.Errorf("@tags([alpha, beta]) not applied, got: %v", op.Tags)
	}
}

// Stacked exclusive bounds (@gt + @positive / @lt + @negative) must
// advertise the TIGHTEST bound (intersect), not the last writer.
func TestExclusiveBoundsIntersect(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"ct/ct.craftgo": `package ct
type T {
  a int @gt(5) @positive
  b int @positive @gt(5)
  c int @lt(-5) @negative
}
service S { post M /m { request T  response T } }`,
	}, &config.Config{})
	s := doc.Components.Schemas["T"].Value
	for _, f := range []string{"a", "b"} {
		got := s.Properties[f].Value.Extensions["exclusiveMinimum"]
		if got != float64(5) {
			t.Errorf("field %s exclusiveMinimum = %v, want 5 (tightest)", f, got)
		}
	}
	if got := s.Properties["c"].Value.Extensions["exclusiveMaximum"]; got != float64(-5) {
		t.Errorf("field c exclusiveMaximum = %v, want -5 (tightest)", got)
	}
}

// A bodyless error must advertise the {code, message} envelope the runtime
// actually returns (not an empty object).
func TestBodylessErrorEnvelopeSchema(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"app/app.craftgo": `package app
error NotFound RecordNotFound
type Req { id string @path }
type Item { id string }
service App {
  @errors(RecordNotFound)
  get One /app/{id} { request Req  response Item }
}`,
	}, &config.Config{})
	s := doc.Components.Schemas["RecordNotFoundErr"].Value
	if s.Properties["code"] == nil || s.Properties["message"] == nil {
		t.Errorf("bodyless error schema missing code/message envelope: %+v", s.Properties)
	}
}

// A declared apiKey security scheme must be emitted with its config shape,
// not the hardcoded http/bearer default.
func TestSecuritySchemeFromConfig(t *testing.T) {
	cfg := &config.Config{}
	cfg.OpenAPI.SecuritySchemes = map[string]config.SecurityScheme{
		"MFA": {Type: "apiKey", In: "header", Name: "X-MFA-Token"},
	}
	doc := genDoc(t, map[string]string{
		"s/s.craftgo": `package s
type Out { ok bool }
service S {
  @security(MFA)
  get A /a { response Out }
}`,
	}, cfg)
	scheme := doc.Components.SecuritySchemes["MFA"]
	if scheme == nil || scheme.Value == nil {
		t.Fatal("MFA scheme not registered")
	}
	if scheme.Value.Type != "apiKey" || scheme.Value.In != "header" || scheme.Value.Name != "X-MFA-Token" {
		t.Errorf("MFA scheme not emitted from config: type=%q in=%q name=%q", scheme.Value.Type, scheme.Value.In, scheme.Value.Name)
	}
}

// @example(<enum-member>) must resolve to the member's wire value in the
// spec, like @default does (it was silently dropped before).
func TestExampleEnumMemberResolved(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"m/m.craftgo": `package m
enum Color { Red Green Blue }
type Thing { c Color @example(Green) }
type Req { id string @path }
service Svc { get Fetch /t/{id} { request Req  response Thing } }`,
	}, &config.Config{})
	thing := doc.Components.Schemas["Thing"].Value
	c := thing.Properties["c"]
	if c == nil || c.Value == nil || c.Value.Example == nil {
		t.Fatalf("@example(Green) dropped from enum field: %+v", c)
	}
	if c.Value.Example != "Green" {
		t.Errorf("@example(Green) = %v, want wire value \"Green\"", c.Value.Example)
	}
}

// A required any[] field must NOT get a runtime nil presence check (matching
// every other required nilable slice).
func TestRequiredAnyArrayNoPresenceCheck(t *testing.T) {
	root, files := projectFiles(t, map[string]string{
		"m/m.craftgo": `package m
type Body { reqStrArr string[]  reqAnyArr any[] }
type Resp { ok bool }
service S { post Op /x { request Body  response Resp } }`,
	})
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("semantic: %v", diags)
	}
	dir := t.TempDir()
	mPkg := proj.Packages["m"]
	if err := GenerateValidatorsAll(mPkg, dir, CrossPkg{}, BuildScalarTable(proj, "m"), BuildTypeTable(proj, "m"), BuildEnumTable(proj, "m")); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "m", "validate.go"))
	if strings.Contains(string(out), "ReqAnyArr == nil") {
		t.Errorf("any[] field wrongly got a nil presence check:\n%s", out)
	}
}

// #5 (M6): two distinct cross-package decls that disambiguate to the same
// merged component name (shared.User -> SharedUser, colliding with a real
// api.SharedUser) are rejected instead of one silently overwriting the other.
func TestCrossPkgMergeNameCollisionRejected(t *testing.T) {
	root, files := projectFiles(t, map[string]string{
		"shared/s.craftgo": `package shared
type User { a string }`,
		"api/a.craftgo": `package api
import "shared"
type User { b int }
type SharedUser { c bool }
type Resp { y shared.User  z SharedUser }
type Req { ok bool }
service S { get Do /do { request Req  response Resp } }`,
	})
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("semantic: %v", diags)
	}
	if dups := projectMergeCollisions(proj); len(dups) == 0 {
		t.Error("expected cross-pkg merge name collision (shared.User vs api.SharedUser)")
	}
}

// #4 (M6): two structurally distinct generic instances that collapse to the
// same component name (Page<IntArray> and Page<int[]> both -> PageOfIntArray)
// are rejected; structurally distinct args that DON'T collide stay clean.
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
		return buildOpenAPIDoc(mergeProjectForOpenAPI(proj), &config.Config{})
	}
	if _, err := mk("real Page<IntArray>  prim Page<int[]>"); err == nil || !strings.Contains(err.Error(), "structurally distinct generic") {
		t.Errorf("expected generic-instance collision error, got: %v", err)
	}
	if _, err := mk("a Page<int>  b Page<string>"); err != nil {
		t.Errorf("distinct generic instances wrongly rejected: %v", err)
	}
}
