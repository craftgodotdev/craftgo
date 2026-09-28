package docs

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/config"
)

// hiddenDesign hides a method, a service and an extend block. Secret, Filter,
// Level, Missing, PageOfSecret and the Internal scheme reach only hidden
// methods; Shared and Bearer reach a visible one too, Token is reached by the
// unused Keep, and Orphan by nothing.
const hiddenDesign = `package app
type Shared { id string }
type Secret { key string }
type Token { v string }
type Keep { tok Token }
type Orphan { note string }
type Page<T> { items T[] }
enum Level {
    Low
    High
}
type Filter { level Level @query }
error NotFound Missing
error Conflict Clash

@security(Bearer)
service Public {
    get List /items { response Page<Shared> }
    @errors(Clash)
    post Make /items { request Shared  response Shared }
    @hidden
    @security(Internal)
    @errors(Missing)
    get Peek /peek { request Filter  response Page<Secret> }
}

@hidden
@security(Internal)
service Ops {
    post Rotate /rotate { request Secret  response Shared }
    post Mint /mint { response Token }
}

@hidden
extend service Public {
    post Drop /drop { request Secret }
}`

// A hidden method leaves the document with each schema and security scheme
// only hidden methods use; what a visible method or no method uses stays.
func TestHiddenMethodsLeaveTheDocument(t *testing.T) {
	doc := genDoc(t, map[string]string{"app/app.craftgo": hiddenDesign}, &config.Config{})
	paths := slices.Sorted(maps.Keys(doc.Paths.Map()))
	if !slices.Equal(paths, []string{"/items"}) {
		t.Errorf("paths = %v, want only /items", paths)
	}
	schemas := slices.Sorted(maps.Keys(doc.Components.Schemas))
	want := []string{"ClashErr", "Keep", "ListRespBody", "MakeReqBody", "MakeRespBody", "Orphan", "PageOfShared", "Shared", "Token"}
	if !slices.Equal(schemas, want) {
		t.Errorf("schemas = %v\nwant      %v", schemas, want)
	}
	schemes := slices.Sorted(maps.Keys(doc.Components.SecuritySchemes))
	if !slices.Equal(schemes, []string{"Bearer"}) {
		t.Errorf("security schemes = %v, want [Bearer]", schemes)
	}
}

// The document of hidden methods refs no schema it lacks.
func TestHiddenMethodsLeaveNoDanglingRef(t *testing.T) {
	body := generateOpenAPIToString(t, hiddenDesign)
	declared := declaredSchemaNames(body)
	for _, ref := range schemaRefNames(body) {
		if !declared[ref] {
			t.Errorf("$ref %q has no component schema\n%s", ref, body)
		}
	}
	mustContainNone(t, body, "Peek", "Rotate", "Mint", "Drop", "Internal", "Secret", "Missing")
}

// A hidden method shares an OpenAPI path with a visible one without an error:
// the document holds only the visible one.
func TestHiddenMethodSharesAPathWithAVisibleOne(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"app/app.craftgo": `package app
type Resp { ok bool }
@prefix("/{$}")
service S { get A / { response Resp } }
@hidden
service T { get B / { response Resp } }`,
	}, &config.Config{})
	op := doc.Paths.Value("/").Get
	if op == nil || op.OperationID != "A" {
		t.Fatalf("GET / = %+v, want operation A", op)
	}
}

// Hiding every method leaves an empty document of the unused declarations.
func TestEveryMethodHidden(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"app/app.craftgo": `package app
type Req { id string }
type Unused { id string }
@hidden
service S { post Make /m { request Req } }`,
	}, &config.Config{})
	if n := doc.Paths.Len(); n != 0 {
		t.Errorf("paths = %d, want none", n)
	}
	schemas := slices.Sorted(maps.Keys(doc.Components.Schemas))
	if !slices.Equal(schemas, []string{"Unused"}) {
		t.Errorf("schemas = %v, want [Unused]", schemas)
	}
	if body, err := marshalDocument(doc); err != nil || !strings.Contains(string(body), "paths: {}") {
		t.Errorf("document = %s (%v), want empty paths", body, err)
	}
}
