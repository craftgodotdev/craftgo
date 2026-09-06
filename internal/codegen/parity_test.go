package codegen

import (
	"reflect"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/config"
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
