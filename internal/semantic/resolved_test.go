package semantic

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// ResolveField reports a field's category, primitive and nilability; a
// qualified type resolves in its own package.
func TestResolveField(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/shared.craftgo": `package shared
scalar Blob bytes
scalar RawDoc bytes @format(raw)
scalar Cents int
enum Tone { Warm Cool }
enum Tier { Bronze = 1  Gold = 2 }`,
		"m/m.craftgo": `package m
import "shared"
scalar Blob bytes
scalar Email string
enum Color { Red Green }
enum Level { Low = 1  High = 2 }
type Inner { x int }
type T {
  s      string
  b      bytes
  rawdoc bytes @format(raw)
  blob   Blob
  email  Email
  c      Color
  inner  Inner
  arr    string[]
  mp     map<string, int>
  lvl    Level
  xblob  shared.Blob
  xraw   shared.RawDoc
  xcents shared.Cents
  xtone  shared.Tone
  xtier  shared.Tier
}`,
	})
	proj, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("semantic: %v", diags)
	}
	m := proj.Packages["m"]
	byName := map[string]*ast.Field{}
	for _, mem := range m.Types["T"].Body {
		if f, ok := mem.(*ast.Field); ok {
			byName[f.Name] = f
		}
	}
	check := func(field string, cat FieldCategory, prim string, nilable bool) {
		t.Helper()
		rf := ResolveField(byName[field], m, proj)
		if rf.Category != cat || rf.ResolvedPrim != prim || rf.IsNilable != nilable {
			t.Errorf("%s: got {cat:%d prim:%q nilable:%v}; want {cat:%d prim:%q nilable:%v}",
				field, rf.Category, rf.ResolvedPrim, rf.IsNilable, cat, prim, nilable)
		}
	}
	check("s", CatPrimitive, "string", false)
	check("b", CatBytes, "bytes", true)
	// A raw field is a nilable wire.Raw, however it is spelt.
	check("rawdoc", CatRawBytes, "bytes", true)
	check("blob", CatScalar, "bytes", true)
	check("email", CatScalar, "string", false)
	check("c", CatEnum, "string", false) // enum backing primitive, resolved like a scalar's
	check("lvl", CatEnum, "int", false)
	check("inner", CatStruct, "", false)
	check("arr", CatArray, "", true)
	check("mp", CatMap, "", true)
	check("xblob", CatScalar, "bytes", true)
	check("xraw", CatRawBytes, "bytes", true)
	check("xcents", CatScalar, "int", false)
	check("xtone", CatEnum, "string", false) // cross-package enum: backing read from its package
	check("xtier", CatEnum, "int", false)
}

// ErrorHasJSONMember counts a field on the JSON body, a mixin's included; a
// header, cookie or @sensitive field rides elsewhere.
func TestErrorHasJSONMember(t *testing.T) {
	pkg := mustClean(t, `package p
type Wait { seconds int @header("X-Wait") }
type Hint { hint string }
error NotFound Bare
error TooManyRequests Header { retryAfter int @header("Retry-After") }
error Unauthorized Cookie { session string @cookie("sid") }
error BadRequest Hidden { internal string @sensitive }
error ServiceUnavailable HeaderMixin { Wait }
error Gone BodyMixin { Hint }
error Unauthorized Optional { code string? }
error Conflict Mixed { etag string @header("ETag")  reason string? }`)
	r := PackageResolver(pkg)
	for name, want := range map[string]bool{
		"Bare": false, "Header": false, "Cookie": false, "Hidden": false, "HeaderMixin": false,
		"BodyMixin": true, "Optional": true, "Mixed": true,
	} {
		if got := ErrorHasJSONMember(pkg.Errors[name], r); got != want {
			t.Errorf("%s: ErrorHasJSONMember = %v, want %v", name, got, want)
		}
	}
}
