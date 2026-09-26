package semantic

import (
	"strings"
	"testing"
)

func TestJSONKeyMayDifferFromTheFieldName(t *testing.T) {
	mustClean(t, `type Item { id string }
type Order { items Item[] @json("OrderItem")  storeId string @json("store_id") }`)
}

func TestTwoFieldsMayNotShareAJSONKey(t *testing.T) {
	expectError(t, `type Order { a string @json("id")  id string }`, CodeFieldNameCollision)
}

// A mixin field counts toward a body's JSON keys; the host's own field of a
// clashing pair is the one reported, whichever comes first.
func TestAMixinFieldMayNotShareAJSONKey(t *testing.T) {
	for _, body := range []string{"Base  identifier string @json(\"id\")", "identifier string @json(\"id\")  Base"} {
		d := expectError(t, "type Base { id string }\ntype R { "+body+" }", CodeFieldNameCollision)
		if d.Pos.Line != 2 || !strings.Contains(d.Msg, `"identifier"`) || len(d.Related) != 1 || d.Related[0].Pos.Line != 1 {
			t.Errorf("%s: want R.identifier reported, related to Base.id; got %v", body, d)
		}
	}
	// Two mixins giving one key, and a mixin from another package.
	expectCodeCount(t, "type A { id string }\ntype B { key string @json(\"id\") }\ntype R { A  B }", CodeFieldNameCollision, 1)
	root, files := projectFixture(t, map[string]string{
		"shared/s.craftgo": "package shared\ntype Base { id string }",
		"api.craftgo":      "package api\nimport \"shared\"\ntype R { shared.Base  identifier string @json(\"id\") }",
	})
	if _, diags := AnalyzeProject(files, Options{DesignRoot: root}); findCode(diags, CodeFieldNameCollision) == nil {
		t.Errorf("a cross-package mixin field sharing a JSON key passed: %v", diags)
	}
}

func TestJSONKeyMustBeAPlainKey(t *testing.T) {
	expectError(t, `type Order { a string @json("") }`, CodeDecoratorArgValue)
	expectError(t, `type Order { a string @json("has space") }`, CodeDecoratorArgValue)
}

// `@json("-")` would tag the field `json:"-"`, which encoding/json skips,
// while the documents list a key "-"; a server-only field is @sensitive.
func TestJSONKeyIsNotADash(t *testing.T) {
	const src = `type Order { a string @json("-")  b string }`
	d := expectError(t, src, CodeDecoratorArgValue)
	expectMessage(t, d, `@json("-")`, "@sensitive")
	expectCodeCount(t, src, CodeDecoratorArgValue, 1)
	mustClean(t, `type Order { a string @json("-a")  b string @json("b-")  c string @json("--") }`)
}

func TestJSONDoesNotCombineWithAnOffBodyBinding(t *testing.T) {
	expectError(t, `type Req { page int? @query @json("p") }
service S { post Do /do { request Req } }`, CodeDecoratorConflict)
}

func TestJSONDoesNotCombineWithSensitive(t *testing.T) {
	expectError(t, `type Req { secret string @sensitive @json("s") }`, CodeDecoratorConflict)
}
