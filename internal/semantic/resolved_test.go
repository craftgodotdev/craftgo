package semantic

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// ResolveField resolves a field's layer-agnostic facts - including a
// CROSS-PACKAGE scalar's nilability, the resolution the per-package checks
// can't do (the gap behind the cross-pkg-promoted scalar nilability bug).
func TestResolveField(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/shared.craftgo": `package shared
scalar Blob bytes
scalar Cents int`,
		"m/m.craftgo": `package m
import "shared"
scalar Blob bytes
scalar Email string
enum Color { Red Green }
type Inner { x int }
type T {
  s      string
  b      bytes
  blob   Blob
  email  Email
  c      Color
  inner  Inner
  arr    string[]
  mp     map<string, int>
  xblob  shared.Blob
  xcents shared.Cents
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
	check("blob", CatScalar, "bytes", true, "m")    // local scalar over bytes -> nilable
	check("email", CatScalar, "string", false, "m") // local scalar over value -> not nilable
	check("c", CatEnum, "", false, "m")
	check("inner", CatStruct, "", false, "m")
	check("arr", CatArray, "", true, "")
	check("mp", CatMap, "", true, "")
	check("xblob", CatScalar, "bytes", true, "shared") // CROSS-PKG scalar over bytes -> resolved + nilable
	check("xcents", CatScalar, "int", false, "shared")
}
