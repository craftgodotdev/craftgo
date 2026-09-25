package docs

import (
	"reflect"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/config"
)

// `@tags`, `@security` and `@errors` document an operation the same way in
// the variadic `@x(A, B)` and the array `@x([A, B])` form, an error the merge
// renames included.
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
			"t/t.craftgo": `package t
error NotFound E1 { z string }`,
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
	// Both forms document the same error responses.
	for _, code := range []int{404, 409} {
		v, a := opV.Responses.Status(code), opA.Responses.Status(code)
		if v == nil {
			t.Fatalf("@errors(E1, E2) documents no %d response", code)
		}
		if !reflect.DeepEqual(v, a) {
			t.Errorf("@errors parity broken at %d: variadic %+v vs array %+v", code, v, a)
		}
	}
}
