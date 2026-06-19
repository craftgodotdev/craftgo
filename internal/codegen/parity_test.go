package codegen

import (
	"reflect"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// Parity tests assert that a rule behaves IDENTICALLY across the "sibling
// axes" where the same logical input can be spelled two ways. They are the
// net for the decide-once refactor: a future change that handles one
// spelling but not its twin fails here instead of waiting for a scan.

// Axis: variadic `@x(A, B)` vs array-shortcut `@x([A, B])`. Every
// AllowArrayShortcut decorator now flattens through ast.DecoratorArgValues,
// so the two spellings must produce byte-identical operation output.
func TestParityDecoratorArrayShortcut(t *testing.T) {
	src := func(tagForm, secForm, errForm string) map[string]string {
		return map[string]string{
			"s/s.craftgo": `package s
error NotFound E1 { a string }
error Conflict E2 { b string }
type Out { ok bool }
@tags(` + tagForm + `)
@security(` + secForm + `)
service S {
  @errors(` + errForm + `)
  get M /m { response Out }
}`,
		}
	}
	variadic := genDoc(t, src("alpha, beta", "Bearer, Admin", "E1, E2"), &config.Config{})
	array := genDoc(t, src("[alpha, beta]", "[Bearer, Admin]", "[E1, E2]"), &config.Config{})

	opV := variadic.Paths.Find("/m").Get
	opA := array.Paths.Find("/m").Get

	if !reflect.DeepEqual(opV.Tags, opA.Tags) {
		t.Errorf("@tags parity broken: variadic %v vs array %v", opV.Tags, opA.Tags)
	}
	if !reflect.DeepEqual(opV.Security, opA.Security) {
		t.Errorf("@security parity broken: variadic %v vs array %v", opV.Security, opA.Security)
	}
	// Error responses: the same status codes must be present in both.
	for _, code := range []int{404, 409} {
		if (opV.Responses.Status(code) == nil) != (opA.Responses.Status(code) == nil) {
			t.Errorf("@errors parity broken at %d: variadic present=%v vs array present=%v",
				code, opV.Responses.Status(code) != nil, opA.Responses.Status(code) != nil)
		}
	}
}

// Axis: the nilability fact, decided in two layers. The semantic resolved IR
// ([semantic.ResolveField].IsNilable) drives the cross-field presence check;
// codegen's [goFieldType] drives the `*T` pointer-wrap. Both must agree that a
// field's Go type already holds nil - otherwise the design-time check and the
// emitted struct disagree (the cross-package-promoted scalar nilability class).
// Both now read [semantic.NilableScalarPrimitive] for the scalar atom; this
// test pins the WHOLE verdict across every field shape, local and cross-package,
// so a future change to either layer's nilability logic fails here.
func TestParityNilabilityIRvsCodegen(t *testing.T) {
	root, files := projectFiles(t, map[string]string{
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
  a      any
  fl     file
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
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("semantic: %v", diags)
	}
	mPkg := proj.Packages["m"]
	r := BuildProjectResolver(proj, newFixtureConfig(), "m")

	for _, mem := range mPkg.Types["T"].Body {
		f, ok := mem.(*ast.Field)
		if !ok {
			continue
		}
		semNilable := semantic.ResolveField(f, mPkg, proj).IsNilable
		// Codegen's wrap-skip: the base Go type already holds nil (syntactic
		// slice/map/pointer/interface, or a scalar over a nilable primitive).
		clone := *f.Type
		clone.Optional = false
		cgNilable := isNilableGoType(GoTypeRef(&clone)) || scalarRefNilable(f.Type, mPkg, r)
		if semNilable != cgNilable {
			t.Errorf("field %q nilability drift: semantic IR=%v, codegen wrap-skip=%v",
				f.Name, semNilable, cgNilable)
		}
	}
}
