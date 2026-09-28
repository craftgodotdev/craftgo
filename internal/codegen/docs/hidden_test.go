package docs

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/semantic"
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

// A declaration a hidden method reaches only where the document writes it
// in place - a map key, a response cookie, a basePath variable - leaves with it.
func TestHiddenInlineUsesLeave(t *testing.T) {
	const basePath = "/api/{tenant}"
	root, files := projectFiles(t, map[string]string{
		"app/app.craftgo": `package app
enum Key {
    A
    B
}
enum Mood {
    Glad
    Sad
}
enum Region {
    Eu
    Us
}
type Counts { counts map<Key, int> }
type Tagged {
    ok   bool
    mood Mood @cookie("mood")
}
type Ten { tenant string @path }
type Scoped { tenant Region @path }
type Resp { ok bool }
service S {
    get Shown /shown { request Ten  response Resp }
    @hidden
    get Count /count { request Ten  response Counts }
    @hidden
    get Tag /tag { request Ten  response Tagged }
    @hidden
    get Scope /scope { request Scoped  response Resp }
}`,
	})
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root, BasePath: basePath})
	for _, d := range diags {
		if d.Severity == lexer.SeverityError {
			t.Fatalf("semantic: %v", d)
		}
	}
	doc, err := buildProjectDocument(proj, &config.Config{OpenAPI: config.OpenAPI{BasePath: basePath}})
	if err != nil {
		t.Fatal(err)
	}
	schemas := slices.Sorted(maps.Keys(doc.Components.Schemas))
	if want := []string{"Resp", "ShownRespBody", "Ten"}; !slices.Equal(schemas, want) {
		t.Errorf("schemas = %v, want %v", schemas, want)
	}
}

// A mixin a documented method's generic request reaches stays, though the
// request writes its fields in place and a hidden method answers it.
func TestHiddenKeepsMixinOfShownGeneric(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"app/app.craftgo": `package app
type Base { b string }
type Item { v string }
type R { ok bool }
type Box<T> {
    Base
    q    string @query
    item T
}
service S {
    post V /v { request Box<Item>  response R }
    @hidden
    get H /h { response Base }
}`,
	}, &config.Config{})
	if _, ok := doc.Components.Schemas["Base"]; !ok {
		t.Errorf("Base dropped; schemas = %v", slices.Sorted(maps.Keys(doc.Components.Schemas)))
	}
}
