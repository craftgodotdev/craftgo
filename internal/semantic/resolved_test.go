package semantic

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// ResolveField reports a field's category, primitive, nilability and home package.
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
	check := func(field string, cat FieldCategory, prim string, nilable bool, home string) {
		t.Helper()
		rf := ResolveField(byName[field], m, proj)
		if rf.Category != cat || rf.ResolvedPrim != prim || rf.IsNilable != nilable || rf.HomePkg != home {
			t.Errorf("%s: got {cat:%d prim:%q nilable:%v home:%q}; want {cat:%d prim:%q nilable:%v home:%q}",
				field, rf.Category, rf.ResolvedPrim, rf.IsNilable, rf.HomePkg,
				cat, prim, nilable, home)
		}
	}
	check("s", CatPrimitive, "string", false, "")
	check("b", CatBytes, "bytes", true, "")
	// A raw field is a nilable wire.Raw with no home package, however it is spelt.
	check("rawdoc", CatRawBytes, "bytes", true, "")
	check("blob", CatScalar, "bytes", true, "m")
	check("email", CatScalar, "string", false, "m")
	check("c", CatEnum, "string", false, "m") // enum backing primitive, resolved like a scalar's
	check("lvl", CatEnum, "int", false, "m")
	check("inner", CatStruct, "", false, "m")
	check("arr", CatArray, "", true, "")
	check("mp", CatMap, "", true, "")
	check("xblob", CatScalar, "bytes", true, "shared")
	check("xraw", CatRawBytes, "bytes", true, "")
	check("xcents", CatScalar, "int", false, "shared")
	check("xtone", CatEnum, "string", false, "shared") // cross-package enum: backing read from its package
	check("xtier", CatEnum, "int", false, "shared")
}
