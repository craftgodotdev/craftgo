package semantic

import (
	"encoding/json"
	"reflect"
	"strconv"
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
		"api.craftgo":      "package api\ntype R { shared.Base  identifier string @json(\"id\") }",
	})
	if _, diags := AnalyzeProject(files, Options{DesignRoot: root}); findCode(diags, CodeFieldNameCollision) == nil {
		t.Errorf("a cross-package mixin field sharing a JSON key passed: %v", diags)
	}
}

// A `@json` key is accepted exactly when encoding/json writes the field under
// it, rather than ignoring the tag and writing the Go field name.
func TestJSONKeyIsOneEncodingJSONTakes(t *testing.T) {
	emits := func(key string) bool {
		typ := reflect.StructOf([]reflect.StructField{{Name: "F", Type: reflect.TypeFor[string](), Tag: reflect.StructTag("json:" + strconv.Quote(key))}})
		out, err := json.Marshal(reflect.New(typ).Elem().Interface())
		want, _ := json.Marshal(map[string]string{key: ""})
		return err == nil && string(out) == string(want)
	}
	for _, key := range []string{
		"store_id", "it's", "has space", "a,b", `a"b`, `a\b`, "a`b", "€", "°", "😀", "日本", "é",
		"x-y.z", "a\tb", "", "#hash", "!#$%&()*+-./:;<=>?@[]^_{|}~", "a\u2028b",
	} {
		lit := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\t", `\t`).Replace(key)
		src := "type Order { a string @json(\"" + lit + "\") }"
		_, diags := Analyze(parseFiles(t, src))
		if refused := findCode(diags, CodeDecoratorArgValue) != nil; refused == emits(key) {
			t.Errorf("@json(%q): refused %v, but encoding/json writes the key: %v", key, refused, emits(key))
		}
	}
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
