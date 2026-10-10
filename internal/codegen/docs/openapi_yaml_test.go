package docs

import (
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	yamlv3 "gopkg.in/yaml.v3"
	"sigs.k8s.io/yaml"
)

// plainTypedStrings returns each string the YAML out writes plain that YAML
// 1.1 reads as another type.
func plainTypedStrings(t *testing.T, out string) []string {
	t.Helper()
	var root yamlv3.Node
	if err := yamlv3.Unmarshal([]byte(out), &root); err != nil {
		t.Fatal(err)
	}
	var found []string
	var walk func(n *yamlv3.Node)
	walk = func(n *yamlv3.Node) {
		for _, c := range n.Content {
			walk(c)
		}
		if n.Kind != yamlv3.ScalarNode || n.Style != 0 {
			return
		}
		switch n.Tag {
		case "!!int", "!!float", "!!bool", "!!null":
		default:
			if n.Tag != "!!str" || yaml11Typed.MatchString(n.Value) {
				found = append(found, n.Value)
			}
		}
	}
	walk(&root)
	return found
}

// A string YAML 1.1 reads as another type - the value key `=`, the merge key
// `<<`, a timestamp, a malformed binary int - is double-quoted, as a value
// and as a key, while numbers and other strings stay plain.
func TestYAML11TypedStringsAreQuoted(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
enum Op { Eq = "="  Merge = "<<"  Ne = "!=" }
type Shift {
    left  int    @json("<<")
    right int    @json("=")
    at    string @example("2001-12-14 21:59:43.10 -5")
    bin   string @example("0b_")
    ver   string @example("1.2.3")
    op    Op     @example(Merge)
    n     int    @gte(1)
}
service S { post Put /shift { request Shift  response Shift } }`)
	if found := plainTypedStrings(t, body); len(found) > 0 {
		t.Errorf("plain strings YAML 1.1 reads as another type: %q\n%s", found, body)
	}
	mustContainAll(t, body,
		`- "="`, `- "<<"`, "- '!='",
		`"<<":`, `"=":`, `- "<<"`, `- "="`,
		`example: "2001-12-14 21:59:43.10 -5"`, `example: "0b_"`, `example: "<<"`,
		"example: 1.2.3\n", "minimum: 1\n")
}

// A string YAML 1.1 reads as another type, written plain over two lines, is
// double-quoted on the first.
func TestYAML11TypedFoldedStringIsQuoted(t *testing.T) {
	key := strings.Repeat("k", 75)
	out, err := yaml.JSONToYAML([]byte(`{"` + key + `": "2001-12-14 21:59:43.10 -5", "x": 1e-07}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "2001-12-14\n") {
		t.Fatalf("the value is not folded:\n%s", out)
	}
	got, err := respellYAML11(out)
	if err != nil {
		t.Fatal(err)
	}
	if want := key + `: "2001-12-14 21:59:43.10 -5"` + "\nx: 1.0e-07\n"; string(got) != want {
		t.Errorf("respelled:\n%s\nwant:\n%s", got, want)
	}
}

// DEL, a C1 control, NEL, U+FFFE and U+FFFF in a string are written escaped,
// and the document reads back as it was built.
func TestControlCharactersAreEscaped(t *testing.T) {
	const text = "a\u007fb\u0080c\u0085d\u009fe\ufffef\uffffg"
	doc := &openapi3.T{
		OpenAPI: "3.1.0",
		Info:    &openapi3.Info{Title: "t", Version: "1", Description: text},
		Paths:   &openapi3.Paths{},
		Components: &openapi3.Components{Schemas: openapi3.Schemas{
			"T": {Value: &openapi3.Schema{Description: text, Enum: []any{text}}},
		}},
	}
	out, err := marshalDocument(doc)
	if err != nil {
		t.Fatal(err)
	}
	if want := `"a\x7Fb\x80c\Nd\x9Fe\uFFFEf\uFFFFg"`; strings.Count(string(out), want) != 3 {
		t.Errorf("want %s three times:\n%s", want, out)
	}
	var back struct {
		Info struct {
			Description string `yaml:"description"`
		} `yaml:"info"`
	}
	if err := yamlv3.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if back.Info.Description != text {
		t.Errorf("description reads back as %q, want %q", back.Info.Description, text)
	}
}
