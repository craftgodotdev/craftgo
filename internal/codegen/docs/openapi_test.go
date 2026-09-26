package docs

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/getkin/kin-openapi/openapi3"
)

// generateOpenAPIToString returns the YAML document generated from src.
func generateOpenAPIToString(t *testing.T, src string) string {
	t.Helper()
	pkg := analyze(t, src)
	root := t.TempDir()
	if err := genOpenAPI(t, pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(root, "docs", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// A `@sensitive` field stays out of the document.
func TestGenerateOpenAPISensitiveFieldOmitted(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
type Req {
    id        string 
    internal  string  @sensitive
}
service S {
    post Make /m { request Req }
}`)
	if strings.Contains(body, "internal:") {
		t.Errorf("sensitive field 'internal' must not appear in OpenAPI spec, got:\n%s", body)
	}
	if !strings.Contains(body, "id:") {
		t.Errorf("regular field 'id' should still be present, got:\n%s", body)
	}
}

// A string or int enum becomes a component schema its fields $ref.
func TestGenerateOpenAPIEnumSchemasEmitted(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
enum Priority { Low  Normal  High }
enum Tier { Bronze = 1  Silver = 2 }
type Req {
    pri Priority
    tir Tier
}
service S {
    post Make /m {
        request   Req
    }
}`)
	expectGolden(t, "openapi-enum-schemas.yaml", body)
}

// A scalar becomes a component schema its fields $ref.
func TestGenerateOpenAPIScalarSchemasEmitted(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
scalar Email string @format(email)
type Req { addr Email }
service S { post Send /m { request Req } }`)
	expectGolden(t, "openapi-scalar-schemas.yaml", body)
}

// Every constraint family on a scalar reaches its component schema.
func TestGenerateOpenAPIScalarFullConstraints(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
scalar Email     string @format(email) @maxLength(254)
scalar Tag       string @minLength(1) @maxLength(20) @pattern("^[a-z-]+$")
scalar ISO3      string @length(3, 3) @pattern("^[A-Z]{3}$")
scalar Cents     int    @gte(0) @lte(1000000)
scalar Percent   float64 @gte(0) @lte(1)
scalar Step      int    @gt(0) @multipleOf(5)
type Req {
    email   Email
    tag     Tag
    country ISO3
    price   Cents
    ratio   Percent
    step    Step
}
service S { post Send /m { request Req } }`)
	mustContainAll(t, body,
		// Email
		"format: email",
		"maxLength: 254",
		// Tag
		"minLength: 1",
		"maxLength: 20",
		"pattern: ^[a-z-]+$",
		// ISO3
		"minLength: 3",
		"maxLength: 3",
		"pattern: ^[A-Z]{3}$",
		// Cents
		"minimum: 0",
		"maximum: 1000000",
		// Step - @gt(0) is the 3.1 numeric exclusive bound, not a boolean
		"exclusiveMinimum: 0",
		"multipleOf: 5",
	)
}

// A constraint narrowing a scalar field joins its $ref in an allOf, or sits
// beside the anyOf of an optional field.
func TestGenerateOpenAPIScalarRefFieldConstraint(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
scalar Cents int @gte(0)
scalar Tag   string @minLength(1)
type Req {
    amount   Cents  @lte(1000000)
    discount Cents? @lte(100)
    code     Tag    @maxLength(5)
}
service S { post Run /run { request Req } }`)
	mustContainAll(t, body,
		// amount: non-optional → allOf:[{$ref:Cents}, {maximum}]
		"allOf:",
		"maximum: 1000000",
		// code: string-length narrowing on a string scalar ref
		"maxLength: 5",
		// discount: optional → anyOf-nullable wrapper + sibling maximum
		"anyOf:",
		"maximum: 100",
	)
}

// `@errors` adds each error's component schema and a response at its
// category's status.
func TestGenerateOpenAPIErrorsDecorator(t *testing.T) {
	src := `package design
error NotFound BookNotFound
error Conflict DuplicateISBN { sku string }
type BookReq { id string }
type Book { id string }
service S {
    @errors(BookNotFound)
    get GetBook /books/{id} { request BookReq  response Book }
    @errors(DuplicateISBN)
    @status(202)
    post CreateBook /books { request Book  response Book }
}`
	pkg := analyze(t, src)
	root := t.TempDir()
	if err := genOpenAPI(t, pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	body := string(out)

	// Error type schemas are emitted under components.schemas.
	mustContainAll(t, body,
		"BookNotFoundErr:",
	)
	// GetBook response 404 → BookNotFoundErr.
	if !strings.Contains(body, `'#/components/schemas/BookNotFoundErr'`) {
		t.Error("expected BookNotFoundErr ref")
	}
	// @status(202) replaces the POST default of 201.
	if !strings.Contains(body, `"202":`) {
		t.Errorf("expected @status(202) override:\n%s", body)
	}
	// The 202 response carries its reason phrase.
	if !strings.Contains(body, `description: Accepted`) {
		t.Errorf("expected `description: Accepted` for 202 response:\n%s", body)
	}
	// CreateBook also registers 409 (Conflict) for DuplicateISBN.
	if !strings.Contains(body, `"409":`) {
		t.Errorf("expected 409 Conflict response:\n%s", body)
	}
}

// An error's @header field becomes a typed header of its error response.
func TestGenerateOpenAPIErrorResponseHeaders(t *testing.T) {
	src := `package design
error TooManyRequests RateLimited {
    retryAfter int @header("Retry-After")
}
type Req { id string }
type Res { id string }
service S {
    @errors(RateLimited)
    get Get /things/{id} { request Req  response Res }
}`
	pkg := analyze(t, src)
	root := t.TempDir()
	if err := genOpenAPI(t, pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	body := string(out)
	mustContainAll(t, body, `"429":`, "headers:", "Retry-After:")
	// The header keeps the field's type (int → integer).
	if i := strings.Index(body, "Retry-After:"); i >= 0 {
		if !strings.Contains(body[i:min(i+80, len(body))], "type: integer") {
			t.Errorf("Retry-After header should carry an integer schema:\n%s", body[i:min(i+120, len(body))])
		}
	}
}

// A method name two services share gets service-prefixed operationIds and body
// components; `@operationId` renames only the operationId.
func TestGenerateOpenAPIMethodNameCollision(t *testing.T) {
	src := `package design
type A { x string }
type B { y string }
service AService { get List /a { response A } }
service BService { get List /b { response B } }
service CService { @operationId("customList") get List /c { response A } }
service DService { get GetThing /d { response A } }`
	pkg := analyze(t, src)
	root := t.TempDir()
	if err := genOpenAPI(t, pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	body := string(out)
	mustContainAll(t, body,
		// Colliding List -> service-prefixed operationId + RespBody ref.
		"operationId: AServiceList",
		"operationId: BServiceList",
		"AServiceListRespBody:",
		"BServiceListRespBody:",
		"#/components/schemas/AServiceListRespBody",
		"#/components/schemas/BServiceListRespBody",
		// @operationId override wins for the id; component still qualified.
		"operationId: customList",
		"CServiceListRespBody:",
		// Unique method name stays bare.
		"operationId: GetThing",
		"GetThingRespBody:",
	)
	// No bare List operationId or ListRespBody component is left.
	mustContainNone(t, body, "operationId: List\n", "\n    ListRespBody:")
}

// Operations whose body components would share a name, `A.BC` and `AB.C`
// both being ABC, get distinct ones: the operation whose operationId is ABC
// keeps it and the other takes the lowest number no operation holds.
func TestOperationBodyComponentsNeverCollide(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"a/a.craftgo": `package a
type Req { name string }
type Resp { ok bool }
service A {
	@operationId("renamedBC")
	post BC /a/bc { request Req  response Resp }
}
service AB {
	post C /ab/c { request Req  response Resp }
}
service X {
	post BC /x/bc { request Req  response Resp }
	post C /x/c { request Req  response Resp }
}
service Y {
	post ABC2 /y { request Req  response Resp }
}`,
	}, &config.Config{})
	for path, stem := range map[string]string{"/ab/c": "ABC", "/a/bc": "ABC3", "/y": "ABC2"} {
		op := doc.Paths.Find(path).Post
		if got := op.RequestBody.Value.Content.Get(mimeApplicationJSON).Schema.Ref; got != "#/components/schemas/"+stem+"ReqBody" {
			t.Errorf("%s request body refs %q, want %sReqBody", path, got, stem)
		}
		if got := op.Responses.Status(201).Value.Content.Get(mimeApplicationJSON).Schema.Ref; got != "#/components/schemas/"+stem+"RespBody" {
			t.Errorf("%s response body refs %q, want %sRespBody", path, got, stem)
		}
	}
}

// Renumbering skips a number whose body component a declaration names: with
// `ABC2RespBody` and `ABC3ReqBody` declared, the second ABC takes ABC4.
func TestOperationBodyRenumberingSkipsDeclaredNames(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"a/a.craftgo": `package a
type Req { name string }
type Resp { ok bool }
type ABC2RespBody { x int }
enum ABC3ReqBody { on  off }
service A {
	@operationId("renamedBC")
	post BC /a/bc { request Req  response Resp }
}
service AB {
	post C /ab/c { request Req  response Resp }
}
service X {
	post BC /x/bc { request Req  response Resp }
	post C /x/c { request Req  response ABC2RespBody }
}`,
	}, &config.Config{})
	for path, stem := range map[string]string{"/ab/c": "ABC", "/a/bc": "ABC4"} {
		op := doc.Paths.Find(path).Post
		if got := op.RequestBody.Value.Content.Get(mimeApplicationJSON).Schema.Ref; got != "#/components/schemas/"+stem+"ReqBody" {
			t.Errorf("%s request body refs %q, want %sReqBody", path, got, stem)
		}
	}
}

// No document is built with a duplicate operationId: the analyser rejects an
// `@operationId` equal to another method's, in one package or across two.
func TestDuplicateOperationIDRejectedBeforeTheDocument(t *testing.T) {
	for label, src := range map[string]map[string]string{
		"one package": {"a/a.craftgo": `package a
type R { x string }
service AService { @operationId("Lookup") get Find /a { response R } }
service BService { get Lookup /b { response R } }`},
		"two packages": {
			"a/a.craftgo": `package a
type R { x string }
service AService { @operationId("Lookup") get Find /a { response R } }`,
			"b/b.craftgo": `package b
type R { x string }
service BService { get Lookup /b { response R } }`,
		},
	} {
		root, files := projectFiles(t, src)
		_, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root})
		if !slices.ContainsFunc(diags, func(d semantic.Diagnostic) bool {
			return d.Code == semantic.CodeDuplicateOperation && d.Severity == lexer.SeverityError
		}) {
			t.Errorf("%s: want a %s error, got %v", label, semantic.CodeDuplicateOperation, diags)
		}
	}
}

// A type named like a generic instance's component (`PageOfOrder`) fails
// generation.
func TestGenerateOpenAPIComponentNameCollisionErrors(t *testing.T) {
	src := `package design
type Order { id string }
type PageOfOrder { hijacked string }
type Page<T> { items T[] }
type Resp { real Page<Order>  fake PageOfOrder }
service S { get Get /g { response Resp } }`
	pkg := analyze(t, src)
	err := genOpenAPI(t, pkg, sampleConfig(), t.TempDir())
	if err == nil {
		t.Fatal("expected a duplicate-component-schema error, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate component schema") || !strings.Contains(err.Error(), "PageOfOrder") {
		t.Errorf("error should name the colliding component; got: %v", err)
	}
}

// @header and @cookie fields stay out of a type's component schema.
func TestGenerateOpenAPITypeSchemaExcludesHeaderFields(t *testing.T) {
	src := `package design
type ListResp {
    items string
    total int    @header("X-Total-Count")
    sess  string @cookie("sid")
}
type Req { id string }
service S {
    get List /items { request Req  response ListResp }
}`
	pkg := analyze(t, src)
	root := t.TempDir()
	if err := genOpenAPI(t, pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	body := string(out)
	// The value is documented as a response header, not a body property.
	if !strings.Contains(body, "X-Total-Count:") {
		t.Errorf("expected X-Total-Count response header in spec:\n%s", body)
	}
	// Neither field is a body property, one level under `properties:`.
	for _, banned := range []string{"\n        total:", "\n        sess:"} {
		if strings.Contains(body, banned) {
			t.Errorf("header/cookie field leaked into a body schema (%q):\n%s", banned, body)
		}
	}
}

// Without @status a POST with a body documents 201, a GET 200 and a bodiless
// method 204.
func TestGenerateOpenAPISuccessStatusDefaults(t *testing.T) {
	src := `package design
type Req { id string }
type Res { id string }
service S {
    post Create /things { request Req  response Res }
    get Get /things/{id} { request Req  response Res }
    delete Remove /things/{id} { request Req }
}`
	pkg := analyze(t, src)
	root := t.TempDir()
	if err := genOpenAPI(t, pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	body := string(out)

	// POST that returns a body defaults to 201 Created.
	mustContainAll(t, body, `"201":`, "description: Created")
	// GET keeps 200 OK; the bodiless DELETE is 204 No Content.
	mustContainAll(t, body, `"200":`, `"204":`, "description: No Content")
	// Only the GET 200 is described "OK".
	if strings.Count(body, "description: OK") != 1 {
		t.Errorf("expected exactly one `description: OK` (the GET 200):\n%s", body)
	}
}

// Two errors with one status share an `anyOf` response.
func TestGenerateOpenAPISameStatusErrorsMerge(t *testing.T) {
	src := `package design
error Conflict EmailTaken { email string }
error Conflict OwnershipConflict { owner string }
type Req { id string }
type Resp { id string }
service S {
    @errors(EmailTaken, OwnershipConflict)
    post UpdateUser /users/{id} { request Req  response Resp }
}`
	pkg := analyze(t, src)
	root := t.TempDir()
	if err := genOpenAPI(t, pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	body := string(out)
	if !strings.Contains(body, "anyOf:") {
		t.Errorf("expected anyOf for same-status errors:\n%s", body)
	}
	// The anyOf lists each error exactly once.
	anyOfIdx := strings.Index(body, "anyOf:")
	if anyOfIdx < 0 {
		t.Fatalf("anyOf block missing:\n%s", body)
	}
	tail := body[anyOfIdx:]
	if end := strings.Index(tail, "\n            description:"); end > 0 {
		tail = tail[:end]
	}
	refCount := strings.Count(tail, "$ref:")
	if refCount != 2 {
		t.Errorf("anyOf must list exactly 2 $refs (one per declared error), got %d:\n%s", refCount, tail)
	}
	mustContainAll(t, body,
		"EmailTakenErr",
	)
}

// Each body an error sends matches the schema of its status, where errors of
// one category, or an error and a success `@status`, may send bodies more than
// one of their schemas admits: two `{code, message}` envelopes, or an envelope
// and a body of optional fields.
func TestResponsesSharingAStatusAdmitEachBody(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"a/a.craftgo": `package a
error ServiceUnavailable Busy
error ServiceUnavailable Down
error ServiceUnavailable Later { hint string? }
error Conflict Taken
type Maybe { note string? }
service S {
	@errors(Busy, Down, Later)
	get G /g { response Maybe }
	@status(409)
	@errors(Taken)
	post P /p { response Maybe }
}`,
	}, &config.Config{})
	if err := openapi3.NewLoader().ResolveRefsIn(doc, nil); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		path, status, body string
	}{
		{"/g", "503", `{"code":"BUSY","message":"Service unavailable"}`},
		{"/g", "503", `{"code":"DOWN","message":"Service unavailable"}`},
		{"/g", "503", `{"hint":"retry"}`},
		{"/p", "409", `{"code":"TAKEN","message":"Conflict"}`},
		{"/p", "409", `{"note":"n"}`},
	} {
		item := doc.Paths.Find(c.path)
		op := item.Get
		if op == nil {
			op = item.Post
		}
		schema := op.Responses.Value(c.status).Value.Content.Get(mimeApplicationJSON).Schema.Value
		var v any
		if err := json.Unmarshal([]byte(c.body), &v); err != nil {
			t.Fatal(err)
		}
		if err := schema.VisitJSON(v); err != nil {
			t.Errorf("%s %s: body %s fails the schema: %v", c.path, c.status, c.body, err)
		}
	}
}

// A header the responses at one status send under one name, in any letter
// case, keeps each one's type, and its Set-Cookie names each cookie once:
// errors of one category and a success `@status` sharing its code alike.
func TestSharedStatusHeadersKeepEachType(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"a/a.craftgo": `package a
error ServiceUnavailable E1 { a string @header("X-Same")  s string @cookie("sid") }
error ServiceUnavailable E2 { b int @header("X-Same") }
error ServiceUnavailable E3 { c bool @header("x-same")  s string @cookie("sid") }
error ServiceUnavailable E4 { d string @header("X-SAME") }
error Conflict Taken { at string @header("X-At")  s string @cookie("sid") }
type Stamped {
	at   int    @header("X-At")
	sess string @cookie("sess")
	note string?
}
service S {
	@errors(E1, E2, E3, E4)
	get G /g { response Stamped }
	@status(409)
	@errors(Taken)
	post P /p { response Stamped }
}`,
	}, &config.Config{})
	if got := slices.Sorted(maps.Keys(doc.Paths.Find("/g").Get.Responses.Value("503").Value.Headers)); !slices.Equal(got, []string{"Set-Cookie", "X-Same"}) {
		t.Errorf("/g 503 headers = %v, want [Set-Cookie X-Same]", got)
	}
	for _, c := range []struct {
		path, status, header, want string
	}{
		{"/g", "503", "X-Same", `{"anyOf":[{"type":"string"},{"type":"integer"},{"type":"boolean"}]}`},
		{"/p", "409", "X-At", `{"anyOf":[{"type":"integer"},{"type":"string"}]}`},
	} {
		item := doc.Paths.Find(c.path)
		op := item.Get
		if op == nil {
			op = item.Post
		}
		raw, err := json.Marshal(op.Responses.Value(c.status).Value.Headers[c.header].Value.Schema)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != c.want {
			t.Errorf("%s %s header %s = %s, want %s", c.path, c.status, c.header, raw, c.want)
		}
	}
	for path, want := range map[string]string{"/g": "Sets cookies: sid", "/p": "Sets cookies: sess, sid"} {
		item := doc.Paths.Find(path)
		op := item.Get
		if op == nil {
			op = item.Post
		}
		for code, resp := range op.Responses.Map() {
			if code == "200" {
				continue
			}
			if got := resp.Value.Headers["Set-Cookie"].Value.Description; got != want {
				t.Errorf("%s %s Set-Cookie = %q, want %q", path, code, got, want)
			}
		}
	}
}

// `@doc` and a leading comment become descriptions, `@summary` the
// operation's summary.
func TestGenerateOpenAPIDocSummaryDescription(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
// Book represents a catalog entry.
type Book {
    id    string @doc("Stable identifier.")
    title string
}
type BookReq { id string }
service S {
    @doc("Fetch a single book.")
    @summary("Get a book")
    get GetBook /books/{id} { request BookReq  response Book }
}`)
	expectGolden(t, "openapi-doc-summary.yaml", body)
}

// `@example` becomes the field's example and `@nullable` adds "null" to its
// type list.
func TestGenerateOpenAPIExampleNullable(t *testing.T) {
	src := `package design
type T {
    name  string @example("alice")
    age   int    @example(30)
    nick  string @nullable
}
service S { post Create /c { request T  response T } }`
	pkg := analyze(t, src)
	root := t.TempDir()
	if err := genOpenAPI(t, pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	src2 := string(body)

	if !strings.Contains(src2, "example: alice") {
		t.Errorf("expected string example:\n%s", src2)
	}
	if !strings.Contains(src2, "example: 30") {
		t.Errorf("expected int example:\n%s", src2)
	}
	if !strings.Contains(src2, `- "null"`) {
		t.Errorf("expected 3.1 null type entry on @nullable field:\n%s", src2)
	}
}

// Each validator decorator on a field stamps its OpenAPI keyword.
func TestGenerateOpenAPIValidatorConstraints(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
type Order {
    price     int    @range(0, 1000000)
    quantity  int    @gte(1) @lte(999)
    discount  int    @gt(0) @lt(100)
    ratio     float64 @positive
    step      int    @multipleOf(5)
    name      string @length(1, 80)
    code      string @minLength(3) @maxLength(10) @pattern("^[A-Z]+$")
    email     string @format(email)
    tags      string[] @minItems(1) @maxItems(10) @uniqueItems
}
service S { post Make /m { request Order  response Order } }`)
	mustContainAll(t, body,
		// numeric
		"minimum: 0",
		"maximum: 1000000",
		"minimum: 1",
		"maximum: 999",
		// 3.1 exclusive bounds are numbers, not booleans.
		"exclusiveMinimum: 0",
		"exclusiveMaximum: 100",
		"multipleOf: 5",
		// string
		"minLength: 1",
		"maxLength: 80",
		"minLength: 3",
		"maxLength: 10",
		"pattern: ^[A-Z]+$",
		"format: email",
		// array
		"minItems: 1",
		"maxItems: 10",
		"uniqueItems: true",
	)
}

// A file's `@mimeTypes` becomes its multipart `encoding` contentType.
func TestGenerateOpenAPIMultipartMimeTypes(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
type UploadReq {
    userId string @path
    avatar file   @form @maxSize(2MB) @mimeTypes("image/png", "image/jpeg")
    doc    file   @form
}
type Resp { ok bool }
service S {
    post Upload /users/{userId}/avatar { request UploadReq  response Resp }
}`)
	mustContainAll(t, body,
		"multipart/form-data:",
		"format: binary",
		"encoding:",
		"avatar:",
		"contentType: image/png, image/jpeg",
	)
	// A file without @mimeTypes gets no contentType.
	if strings.Contains(body, "doc:\n          contentType") {
		t.Errorf("file without @mimeTypes should not produce contentType:\n%s", body)
	}
}

// The multipart schema requires every non-optional form and file field.
func TestGenerateOpenAPIMultipartRequired(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
type UploadReq {
    userId  string  @path
    avatar  file    @form
    caption string? @form
}
type Resp { ok bool }
service S {
    post Upload /users/{userId}/avatar { request UploadReq  response Resp }
}`)
	i := strings.Index(body, "multipart/form-data:")
	if i < 0 {
		t.Fatalf("no multipart body:\n%s", body)
	}
	// The schema's `required:` is the first after the media type; the
	// requestBody's `required: true` comes later.
	block := body[i:]
	r := strings.Index(block, "required:")
	if r < 0 {
		t.Fatalf("multipart schema has no required[]:\n%s", block)
	}
	reqList := block[r:min(r+48, len(block))]
	if !strings.Contains(reqList, "- avatar") {
		t.Errorf("required file `avatar` must be listed under multipart required[]:\n%s", reqList)
	}
	// `caption` is optional and `userId` is not in the body.
	if strings.Contains(reqList, "caption") {
		t.Errorf("optional form field `caption` must NOT be in multipart required[]:\n%s", reqList)
	}
	if strings.Contains(reqList, "userId") {
		t.Errorf("path-bound `userId` must NOT be in multipart required[]:\n%s", reqList)
	}
}

// An optional multipart part is left out of `required`, never documented as
// null: a form part is sent or not.
func TestMultipartOptionalPartsAreNotNullable(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"a/a.craftgo": `package a
enum Level { low  high }
scalar Code string @minLength(2)
type Up {
	doc     file
	caption string? @maxLength(80)
	count   int?
	level   Level?
	code    Code?   @maxLength(8)
	tags    string[]?
	note    string @nullable
}
type Ok { ok bool }
service S { post U /u { request Up  response Ok } }`,
	}, &config.Config{})
	body := doc.Paths.Find("/u").Post.RequestBody.Value.Content.Get(mimeMultipartFormData).Schema.Value
	want := map[string]string{
		"caption": `{"maxLength":80,"type":"string"}`,
		"count":   `{"type":"integer"}`,
		"level":   `{"$ref":"#/components/schemas/Level"}`,
		"code":    `{"allOf":[{"$ref":"#/components/schemas/Code"},{"maxLength":8}]}`,
		"tags":    `{"items":{"type":"string"},"type":"array"}`,
		"note":    `{"type":"string"}`,
	}
	for name, w := range want {
		raw, err := json.Marshal(body.Properties[name])
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != w {
			t.Errorf("part %s = %s, want %s", name, raw, w)
		}
	}
	if !slices.Equal(body.Required, []string{"note", "doc"}) {
		t.Errorf("required = %v, want [note doc]", body.Required)
	}
}

// An optional parameter or response header is never documented as null: a
// parameter or a header is sent or not, and `required` carries a parameter's
// optionality.
func TestOptionalParametersAndHeadersAreNotNullable(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"a/a.craftgo": `package a
enum Level { low  high }
scalar Code string @minLength(2)
type Find {
	q     string?   @maxLength(80)
	count int?
	level Level?
	code  Code?     @maxLength(8)
	tags  string[]?
	trace string?   @header("X-Trace")
	sid   Level?    @cookie("sid")
}
type Found {
	next string? @header("X-Next")
	ok   bool
}
error TooManyRequests Slow { wait int? @header("Retry-After") }
service S { @errors(Slow) get F /f { request Find  response Found } }`,
	}, &config.Config{})
	op := doc.Paths.Find("/f").Get
	want := map[string]string{
		"q":       `{"maxLength":80,"type":"string"}`,
		"count":   `{"type":"integer"}`,
		"level":   `{"$ref":"#/components/schemas/Level"}`,
		"code":    `{"allOf":[{"$ref":"#/components/schemas/Code"},{"maxLength":8}]}`,
		"tags":    `{"items":{"type":"string"},"type":"array"}`,
		"X-Trace": `{"type":"string"}`,
		"sid":     `{"$ref":"#/components/schemas/Level"}`,
	}
	for _, p := range op.Parameters {
		raw, err := json.Marshal(p.Value.Schema)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != want[p.Value.Name] {
			t.Errorf("parameter %s = %s, want %s", p.Value.Name, raw, want[p.Value.Name])
		}
		if p.Value.Required {
			t.Errorf("parameter %s is required", p.Value.Name)
		}
	}
	if len(op.Parameters) != len(want) {
		t.Errorf("%d parameters, want %d", len(op.Parameters), len(want))
	}
	for _, h := range []struct{ code, name, want string }{
		{"200", "X-Next", `{"type":"string"}`},
		{"429", "Retry-After", `{"type":"integer"}`},
	} {
		raw, err := json.Marshal(op.Responses.Value(h.code).Value.Headers[h.name].Value.Schema)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != h.want {
			t.Errorf("response %s header %s = %s, want %s", h.code, h.name, raw, h.want)
		}
	}
}

// A float the document writes in exponent form carries a dot, `1.0e-07`, which
// YAML 1.1 and 1.2 readers both take for a number, where a YAML 1.1 reader
// takes `1e-07` for a string; no other number changes its spelling.
func TestExponentFloatsCarryADot(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
type T {
	a float64   @gte(0.0000001) @lte(100000000000000000000.0)
	c float64   @lte(0.00000015)
	d float64   @gte(-0.000001)
	e int       @lte(1000000)
	f float64?  @default(0.0000002) @example(0.0000003)
	k float64   @lte(1234567.5) @gte(-0.0001)
	m float64[] @example([0.00001, 0.5])
}
service S { post C /c { request T  response T } }`)
	mustContainAll(t, body,
		"minimum: 1.0e-07", "maximum: 1.0e+20", "maximum: 1.5e-07", "minimum: -1.0e-06",
		"maximum: 1000000", "default: 2.0e-07", "example: 3.0e-07", "maximum: 1.2345675e+06",
		"minimum: -0.0001", "- 1.0e-05\n", "- 0.5\n")
	mustContainNone(t, body, ": 1e-07", ": 1e+20", ": -1e-06", ": 2e-07", ": 3e-07", "- 1e-05")
}

// A multipart request's cross-field constraint wraps its inline schema in an
// allOf.
func TestGenerateOpenAPIMultipartCrossField(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
@mutuallyExclusive(a, b)
type UploadReq {
    a      string?
    b      string?
    avatar file    @form
}
type Resp { ok bool }
service S {
    post Upload /upload { request UploadReq  response Resp }
}`)
	i := strings.Index(body, "multipart/form-data:")
	if i < 0 {
		t.Fatalf("no multipart body:\n%s", body)
	}
	block := body[i:]
	if !strings.Contains(block, "allOf:") || !strings.Contains(block, "not:") {
		t.Errorf("multipart schema must carry the @mutuallyExclusive fragment (allOf + not):\n%s", block[:min(900, len(block))])
	}
}

// A request body listed in place, beside a path variable or a header or as
// multipart parts, carries the cross-field groups of the mixins it embeds,
// nested and generic ones included, which the server's validation runs.
func TestInlineRequestBodiesCarryMixinGroups(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"a/a.craftgo": `package a
@requiresOneOf(email, phone)
type Contact {
	email string? @json("e_mail")
	phone string?
}
@mutuallyExclusive(fax, pager)
type Legacy {
	fax   string?
	pager string?
}
type Reach { Contact }
type Box<T> {
	Contact
	val T
}
type Mixed {
	Reach
	id   string @path
	note string
}
type GenMixed {
	Box<int>
	trace string @header("X-Trace")
}
type Upload {
	Contact
	Legacy
	doc file
}
service S {
	post M /m/{id} { request Mixed  response Contact }
	post G /g { request GenMixed  response Contact }
	post U /u { request Upload  response Contact }
}`,
	}, &config.Config{})
	for name, c := range map[string]struct {
		got  *openapi3.SchemaRef
		want []string
	}{
		"MReqBody":    {doc.Components.Schemas["MReqBody"], []string{"e_mail", "phone"}},
		"GReqBody":    {doc.Components.Schemas["GReqBody"], []string{"e_mail", "phone"}},
		"U multipart": {doc.Paths.Find("/u").Post.RequestBody.Value.Content.Get(mimeMultipartFormData).Schema, []string{"email", "fax", "pager", "phone"}},
	} {
		if got := fragmentKeys(c.got.Value); !slices.Equal(got, c.want) {
			t.Errorf("%s cross-field fragment keys = %v, want %v", name, got, c.want)
		}
	}
}

// A multipart group counts a text part present when it is sent non-empty, as
// the handler binds it, and a file part when it is sent.
func TestMultipartGroupsCountNonEmptyParts(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"a/a.craftgo": `package a
@requiresOneOf(email, phone)
type Contact {
	email string?
	phone int?
}
@mutuallyExclusive(doc, url)
@requiresOneOf(doc, url)
type Up {
	Contact
	doc   file?
	url   string?
	title string
}
type Ok { ok bool }
service S { post U /u { request Up  response Ok } }`,
	}, &config.Config{})
	body := doc.Paths.Find("/u").Post.RequestBody.Value.Content.Get(mimeMultipartFormData).Schema.Value
	var got []string
	for _, frag := range body.AllOf[1:] {
		raw, err := json.Marshal(frag)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, string(raw))
	}
	want := []string{
		`{"anyOf":[{"properties":{"email":{"minLength":1}},"required":["email"]},{"properties":{"phone":{"minLength":1}},"required":["phone"]}]}`,
		`{"not":{"properties":{"url":{"minLength":1}},"required":["doc","url"]}}`,
		`{"anyOf":[{"required":["doc"]},{"properties":{"url":{"minLength":1}},"required":["url"]}]}`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("multipart group fragments:\n got  %s\n want %s", strings.Join(got, "\n      "), strings.Join(want, "\n      "))
	}
}

// @mutuallyExclusive admits at most one of its members, as the validator
// enforces: any two of three fail the type's schema, a JSON body listed in
// place and a multipart body alike.
func TestMutuallyExclusiveAdmitsAtMostOneMember(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"a/a.craftgo": `package a
@mutuallyExclusive(email, sms, push)
type Notify {
	email string?
	sms   string?
	push  string?
}
type ByID {
	Notify
	id string @path
}
type Upload {
	Notify
	doc file
}
type Ok { ok bool }
service S {
	post N /n { request Notify  response Ok }
	post I /i/{id} { request ByID  response Ok }
	post U /u { request Upload  response Ok }
}`,
	}, &config.Config{})
	schemas := map[string]*openapi3.Schema{
		"Notify":      doc.Components.Schemas["Notify"].Value,
		"IReqBody":    doc.Components.Schemas["IReqBody"].Value,
		"U multipart": doc.Paths.Find("/u").Post.RequestBody.Value.Content.Get(mimeMultipartFormData).Schema.Value,
	}
	bodies := map[string]bool{
		`{"email":"e"}`:                      true,
		`{"email":"e","sms":null}`:           true,
		`{"email":"e","sms":"s"}`:            false,
		`{"sms":"s","push":"p"}`:             false,
		`{"email":"e","push":"p"}`:           false,
		`{"email":"e","sms":"s","push":"p"}`: false,
	}
	for name, schema := range schemas {
		for body, valid := range bodies {
			var v map[string]any
			if err := json.Unmarshal([]byte(body), &v); err != nil {
				t.Fatal(err)
			}
			if name == "U multipart" {
				if v["sms"] == nil {
					delete(v, "sms")
				}
				v["doc"] = "d"
			}
			if err := schema.VisitJSON(v); (err == nil) != valid {
				t.Errorf("%s with %s: valid = %v, want %v (%v)", name, body, err == nil, valid, err)
			}
		}
	}
}

// A cross-field group whose members all ride as parameters, the type's own or
// a mixin's, is named in the operation's description.
func TestParameterGroupsAreNamedOnTheOperation(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"a/a.craftgo": `package a
@mutuallyExclusive(ref, code)
type Legacy {
	ref  string?
	code string?
}
@requiresOneOf(byName, byId)
type Find {
	Legacy
	byName string? @json("by_name")
	byId   string? @json("by_id")
}
type R { ok bool }
service S {
	@doc("Finds one.")
	get Find /find { request Find  response R }
}`,
	}, &config.Config{})
	want := "Finds one.\n\n" +
		"At most one of the parameters ref, code may be set.\n\n" +
		"At least one of the parameters byName, byId must be set."
	if got := doc.Paths.Find("/find").Get.Description; got != want {
		t.Errorf("description = %q\nwant          %q", got, want)
	}
}

// A response body listed in place beside a header or a cookie carries the
// cross-field groups of its type and of the mixins it embeds.
func TestHeaderSplitResponseBodyCarriesGroups(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"a/a.craftgo": `package a
@requiresOneOf(email, phone)
type Contact {
	email string? @json("e_mail")
	phone string?
}
@mutuallyExclusive(fax, pager)
type Resp {
	Contact
	etag  string  @header("ETag")
	fax   string?
	pager string?
}
@requiresOneOf(a, b)
type Box<T> {
	Contact
	sess string @cookie("sid")
	a    T?
	b    string?
}
service S {
	get R /r { response Resp }
	get B /b { response Box<int> }
}`,
	}, &config.Config{})
	for name, want := range map[string][]string{
		"RRespBody": {"e_mail", "fax", "pager", "phone"},
		"BRespBody": {"a", "b", "e_mail", "phone"},
	} {
		if got := fragmentKeys(doc.Components.Schemas[name].Value); !slices.Equal(got, want) {
			t.Errorf("%s cross-field fragment keys = %v, want %v", name, got, want)
		}
	}
}

// A type with a mixin is an allOf of the mixin's $ref and its own properties.
func TestGenerateOpenAPIMixinFlatten(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
type Audit { createdAt string @format(datetime)  updatedAt string @format(datetime) }
type User { Audit  id string  name string }
service S { post Create /c { request User  response User } }`)
	if !strings.Contains(body, "allOf:") {
		t.Errorf("mixin host should use allOf:\n%s", body)
	}
	if !strings.Contains(body, "$ref: '#/components/schemas/Audit'") {
		t.Errorf("mixin ref missing:\n%s", body)
	}
	if !strings.Contains(body, "id:") || !strings.Contains(body, "name:") {
		t.Errorf("host properties missing:\n%s", body)
	}
}

// A generic instance is a component of its own, which the field $refs.
func TestGenerateOpenAPIGenericInstanceEmitsComponent(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
type Order { id string }
type Page<T> { items T[]  total int }
type ListResp { page Page<Order> }
service S { get List /things { response ListResp } }`)
	if !strings.Contains(body, "PageOfOrder:") {
		t.Errorf("expected PageOfOrder component schema:\n%s", body)
	}
	if !strings.Contains(body, "$ref: '#/components/schemas/PageOfOrder'") {
		t.Errorf("expected $ref to PageOfOrder from listing site:\n%s", body)
	}
	// Its items $ref the Order component.
	idx := strings.Index(body, "PageOfOrder:")
	if idx < 0 {
		t.Fatal("PageOfOrder not found")
	}
	tail := body[idx : idx+400]
	if !strings.Contains(tail, "$ref: '#/components/schemas/Order'") {
		t.Errorf("PageOfOrder items should $ref Order:\n%s", tail)
	}
}

// A generic instance only a @sensitive field or a header-split response
// names gets no component: nothing in the document refs it.
func TestGenerateOpenAPIUnreferencedGenericInstanceHasNoComponent(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"a/a.craftgo": `package a
type Item { id string }
type Other { id string }
type Page<T> {
	trace string @header("X-Trace")
	items T[]
}
type Box<T> { inner T }
type Holder {
	secret Box<Other> @sensitive
	x      string
}
service S {
	get L /l { response Page<Item> }
	get H /h { response Holder }
}`,
	}, &config.Config{})
	for _, orphan := range []string{"BoxOfOther", "PageOfItem"} {
		if _, ok := doc.Components.Schemas[orphan]; ok {
			t.Errorf("unreferenced instance %s has a component", orphan)
		}
	}
	if _, ok := doc.Components.Schemas["LRespBody"]; !ok {
		t.Error("the header-split response has no LRespBody component")
	}
}

// A request with nothing on the body gets neither a request body nor the
// `<base>ReqBody` and instance components one would ref.
func TestGenerateOpenAPIRequestWithoutBodyHasNoReqBody(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"a/a.craftgo": `package a
type Item { id string }
type Wrap<T> { data T @sensitive }
service S { post P /p { request Wrap<Item> } }`,
	}, &config.Config{})
	if op := doc.Paths.Find("/p").Post; op == nil || op.RequestBody != nil {
		t.Fatalf("POST /p should document no request body: %+v", op)
	}
	for _, name := range []string{"PReqBody", "WrapOfItem"} {
		if _, ok := doc.Components.Schemas[name]; ok {
			t.Errorf("component %s is emitted though nothing refs it", name)
		}
	}
}

// A generic instance keeps its declaration's field constraints and
// description.
func TestGenerateOpenAPIGenericInstanceCarriesFieldMetadata(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
type Order { id string }
// Box wraps a value with a bounded count and a formatted stamp.
type Box<T> {
    item  T
    count int?   @gte(1) @lte(100) @default(10)
    label string @maxLength(64)
    stamp string @format(datetime)
}
type Host { box Box<Order> }
service S { get Get /things { response Host } }`)

	idx := strings.Index(body, "BoxOfOrder:")
	if idx < 0 {
		t.Fatalf("expected BoxOfOrder component schema:\n%s", body)
	}
	block := body[idx:min(idx+700, len(body))]
	for _, want := range []string{
		"minimum: 1",                             // @gte(1)
		"maximum: 100",                           // @lte(100)
		"default: 10",                            // @default(10)
		"maxLength: 64",                          // @maxLength(64)
		"format: date-time",                      // @format(datetime) → standard keyword
		"Box wraps a value with a bounded count", // type-level description
	} {
		if !strings.Contains(block, want) {
			t.Errorf("BoxOfOrder must carry %q (H4: generic instances inherit field/type metadata):\n%s", want, block)
		}
	}
}

// A generic declaration's mixin reaches the instance as an allOf $ref.
func TestGenerateOpenAPIGenericInstanceMixinFlatten(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
type Audit { createdAt string  updatedAt string }
type Order { id string }
type Page<T> { Audit  items T[]  total int }
type ListResp { page Page<Order> }
service S { get List /things { response ListResp } }`)
	if !strings.Contains(body, "PageOfOrder:") {
		t.Fatal("PageOfOrder missing")
	}
	idx := strings.Index(body, "PageOfOrder:")
	tail := body[idx : idx+500]
	if !strings.Contains(tail, "allOf:") {
		t.Errorf("PageOfOrder should use allOf for mixin:\n%s", tail)
	}
	if !strings.Contains(tail, "$ref: '#/components/schemas/Audit'") {
		t.Errorf("PageOfOrder should reference Audit mixin:\n%s", tail)
	}
}

// A recursive generic instance $refs itself.
func TestGenerateOpenAPIRecursiveGenericTerminatesViaRef(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
type Leaf { id string }
type Tree<T> { val T  kids Tree<T>[] }
type Forest { root Tree<Leaf> }
service S { get Get /trees { response Forest } }`)
	if !strings.Contains(body, "TreeOfLeaf:") {
		t.Fatal("TreeOfLeaf missing")
	}
	idx := strings.Index(body, "TreeOfLeaf:")
	tail := body[idx : idx+400]
	// `kids Tree<T>[]` substitutes to `Tree<Leaf>[]`, the same instance.
	if !strings.Contains(tail, "$ref: '#/components/schemas/TreeOfLeaf'") {
		t.Errorf("TreeOfLeaf body should $ref itself in the kids field:\n%s", tail)
	}
}

// An optional generic instance is `anyOf: [{$ref}, {type: null}]`.
func TestGenerateOpenAPIGenericOptionalWraps(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
type Order { id string }
type Page<T> { items T[]  total int }
type Holder { page Page<Order>? }
service S { get Get /h { response Holder } }`)
	idx := strings.Index(body, "Holder:")
	if idx < 0 {
		t.Fatal("Holder missing")
	}
	tail := body[idx : idx+400]
	if !strings.Contains(tail, "anyOf:") {
		t.Errorf("Holder.page should use anyOf wrapper for optional ref:\n%s", tail)
	}
	if !strings.Contains(tail, "$ref: '#/components/schemas/PageOfOrder'") {
		t.Errorf("Holder.page should $ref the generic instance:\n%s", tail)
	}
	if !strings.Contains(tail, `type: "null"`) {
		t.Errorf("Holder.page should compose with the 3.1 null type:\n%s", tail)
	}
}

// `response Page<Order>` makes `<base>RespBody` $ref `PageOfOrder`.
func TestGenerateOpenAPIGenericResponseTopLevel(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
type Order { id string }
type Page<T> { items T[]  total int }
service S { get List /things { response Page<Order> } }`)
	if !strings.Contains(body, "ListRespBody:") {
		t.Fatal("ListRespBody missing")
	}
	idx := strings.Index(body, "ListRespBody:")
	tail := body[idx : idx+200]
	if !strings.Contains(tail, "$ref: '#/components/schemas/PageOfOrder'") {
		t.Errorf("ListRespBody should $ref PageOfOrder, got:\n%s", tail)
	}
}

// `Pair<Order, ProductRef>` is the component `PairOfOrderAndProductRef`.
func TestGenerateOpenAPIGenericMultiParam(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
type Order { id string }
type ProductRef { sku string }
type Pair<A, B> { left A  right B }
type Resp { pair Pair<Order, ProductRef> }
service S { get Get /p { response Resp } }`)
	if !strings.Contains(body, "PairOfOrderAndProductRef:") {
		t.Errorf("expected PairOfOrderAndProductRef:\n%s", body)
	}
}

// A service's `@security` applies to every operation; a method's own adds an
// alternative requirement.
func TestGenerateOpenAPIServiceLevelSecurityInherit(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
@security(Bearer)
service S {
    @doc("inherits service-level Bearer")
    get A /a {}
    @doc("inherits Bearer plus its own Admin alt")
    @security(Admin)
    get B /b {}
}`)
	aBlock := operationBlock(t, body, "A")
	if !strings.Contains(aBlock, "Bearer:") {
		t.Errorf("operation A missing inherited Bearer security:\n%s", aBlock)
	}
	bBlock := operationBlock(t, body, "B")
	if !strings.Contains(bBlock, "Bearer:") {
		t.Errorf("operation B missing inherited Bearer:\n%s", bBlock)
	}
	if !strings.Contains(bBlock, "Admin:") {
		t.Errorf("operation B missing method-level Admin:\n%s", bBlock)
	}
}

// `@ignoreSecurity` drops the service's `@security` from the operation.
func TestGenerateOpenAPIIgnoreSecurityClearsInherited(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
@security(Bearer)
service S {
    @doc("inherits Bearer")
    get Authed /a {}
    @doc("public endpoint, opts out of inherited security")
    @ignoreSecurity
    get Public /p {}
}`)
	authedBlock := operationBlock(t, body, "Authed")
	if !strings.Contains(authedBlock, "Bearer:") {
		t.Errorf("Authed should inherit Bearer:\n%s", authedBlock)
	}
	publicBlock := operationBlock(t, body, "Public")
	if strings.Contains(publicBlock, "Bearer:") {
		t.Errorf("Public should have cleared Bearer:\n%s", publicBlock)
	}
}

// `@ignoreTags` drops the service's tags and keeps the method's own.
func TestGenerateOpenAPIIgnoreTagsClearsInherited(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
@tags("users")
service S {
    get WithUsers /u {}
    @ignoreTags
    @tags("admin")
    get OnlyAdmin /a {}
}`)
	withUsersBlock := operationBlock(t, body, "WithUsers")
	if !strings.Contains(withUsersBlock, "users") {
		t.Errorf("WithUsers should inherit users tag:\n%s", withUsersBlock)
	}
	onlyAdminBlock := operationBlock(t, body, "OnlyAdmin")
	if strings.Contains(onlyAdminBlock, "users") {
		t.Errorf("OnlyAdmin should have cleared users tag:\n%s", onlyAdminBlock)
	}
	if !strings.Contains(onlyAdminBlock, "admin") {
		t.Errorf("OnlyAdmin should keep its own admin tag:\n%s", onlyAdminBlock)
	}
}

// operationBlock returns the YAML of operation opID, from its verb line to
// the next verb or path.
func operationBlock(t *testing.T, body, opID string) string {
	t.Helper()
	idx := strings.Index(body, "\n      operationId: "+opID)
	if idx < 0 {
		t.Fatalf("operation %q not found in:\n%s", opID, body)
	}
	// The backward search includes idx: the operationId match can start at
	// the verb line's own newline.
	verbs := []string{"\n    get:\n", "\n    post:\n", "\n    put:\n", "\n    patch:\n", "\n    delete:\n"}
	start := -1
	for _, v := range verbs {
		if s := strings.LastIndex(body[:idx+1], v); s > start {
			start = s
		}
	}
	if start < 0 {
		start = idx
	}
	// The block ends at the next verb or path entry.
	searchFrom := idx + 1
	end := len(body)
	for _, v := range verbs {
		if s := strings.Index(body[searchFrom:], v); s >= 0 && searchFrom+s < end {
			end = searchFrom + s
		}
	}
	// New path entry: line starts with `  /` after a newline.
	if s := strings.Index(body[searchFrom:], "\n  /"); s >= 0 && searchFrom+s < end {
		end = searchFrom + s
	}
	return body[start:end]
}

// Response @header fields become headers, @cookie fields one Set-Cookie
// header, and only the rest form the body.
func TestGenerateOpenAPIResponseHeaders(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
type Resp { items string  total string @header("X-Total-Count")  session string @cookie("sid") }
service S { get List /things { response Resp } }`)
	if !strings.Contains(body, "X-Total-Count:") {
		t.Errorf("X-Total-Count header missing:\n%s", body)
	}
	if !strings.Contains(body, "Set-Cookie:") {
		t.Errorf("Set-Cookie header missing:\n%s", body)
	}
	if !strings.Contains(body, "Sets cookies: sid") {
		t.Errorf("cookie names hint missing:\n%s", body)
	}
	// ListRespBody lists only `items`.
	if strings.Contains(body, "total:") && strings.Contains(body, "ListRespBody:\n      properties:\n        items:") {
		idx := strings.Index(body, "ListRespBody:")
		if idx >= 0 {
			tail := body[idx:]
			if end := strings.Index(tail, "type: object"); end >= 0 {
				snippet := tail[:end]
				if strings.Contains(snippet, "total:") || strings.Contains(snippet, "session:") {
					t.Errorf("header/cookie field leaked into RespBody:\n%s", snippet)
				}
			}
		}
	}
}

// An error's mixin reaches its schema as an allOf $ref.
func TestGenerateOpenAPIErrorMixinFlatten(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
type Audit { createdAt string @format(datetime)  updatedAt string @format(datetime) }
error NotFound BookNotFound { Audit  sku string }
type BookReq { id string }
type Book { id string }
service S { @errors(BookNotFound) get GetBook /b/{id} { request BookReq  response Book } }`)
	if !strings.Contains(body, "BookNotFoundErr:") {
		t.Fatalf("error schema missing:\n%s", body)
	}
	if !strings.Contains(body, "allOf:") {
		t.Errorf("error mixin host should use allOf:\n%s", body)
	}
	if !strings.Contains(body, "$ref: '#/components/schemas/Audit'") {
		t.Errorf("Audit mixin ref missing on error schema:\n%s", body)
	}
}

// An optional named-type field is `anyOf: [{$ref}, {type: null}]`; a plain
// one stays a bare $ref.
func TestGenerateOpenAPIOptionalRefNullable(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
type Inner { id string }
type T {
    plain    Inner
    optional Inner?
}
service S { post Create /c { request T  response T } }`)
	if !strings.Contains(body, "$ref: '#/components/schemas/Inner'") {
		t.Errorf("plain ref missing:\n%s", body)
	}
	if !strings.Contains(body, `type: "null"`) {
		t.Errorf("optional ref should carry the 3.1 null type:\n%s", body)
	}
	if !strings.Contains(body, "anyOf:") {
		t.Errorf("optional ref should use anyOf wrapper:\n%s", body)
	}
}

// `T?` and `@nullable` both add "null" to the type list; only `T?` leaves
// `required`.
func TestGenerateOpenAPIOptionalEmitsNullable(t *testing.T) {
	src := `package design
type T {
    a string?
    b string  @nullable
    c string
}
service S { post Create /c { request T  response T } }`
	pkg := analyze(t, src)
	root := t.TempDir()
	if err := genOpenAPI(t, pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	out := string(body)

	// `a` and `b` in both T and CreateReqBody: four "null" entries.
	if got := strings.Count(out, `- "null"`); got < 4 {
		t.Errorf("expected the 3.1 null type for T? and @nullable in BOTH T and CreateReqBody (got %d):\n%s", got, out)
	}
	// `c` and `b` stay required, `a` does not, in both schemas.
	if got := strings.Count(out, "- c\n"); got < 2 {
		t.Errorf("c must remain in required[] for T and CreateReqBody (got %d):\n%s", got, out)
	}
	if got := strings.Count(out, "- b\n"); got < 2 {
		t.Errorf("@nullable field b must remain in required[] (got %d):\n%s", got, out)
	}
	if strings.Contains(out, "- a\n") {
		t.Errorf("T? field a must be dropped from required[]:\n%s", out)
	}
}

// The per-operation body schema carries the same field metadata as the
// type's component.
func TestGenerateOpenAPIPerOperationSchemaMetadata(t *testing.T) {
	src := `package design
type T {
    // The display name.
    name  string @example("alice")
    age   int?   @default(18)
    nick  string @nullable
    old   string @deprecated("use name instead")
}
service S { post Create /c { request T  response T } }`
	pkg := analyze(t, src)
	root := t.TempDir()
	if err := genOpenAPI(t, pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	out := string(body)
	// Each marker appears under both T and CreateReqBody.
	for _, want := range []string{
		"example: alice",
		"default: 18",
		`- "null"`,
		"deprecated: true",
		"The display name",
	} {
		got := strings.Count(out, want)
		if got < 2 {
			t.Errorf("expected %q in BOTH T and CreateReqBody (got %d occurrences):\n%s", want, got, out)
		}
	}
}

// @deprecated marks a type's schema, a field's property or an operation, and
// its reason joins the description.
func TestGenerateOpenAPIDeprecated(t *testing.T) {
	src := `package design
@deprecated
type LegacyBook { title string  sku string @deprecated("use ISBN") }
service S {
    @deprecated("use NewList")
    get LegacyList /legacy {
        response  LegacyBook
    }
    get NewList /new {
        response  LegacyBook
    }
}`
	pkg := analyze(t, src)
	root := t.TempDir()
	if err := genOpenAPI(t, pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	body := string(out)

	// Type-level: the marker sits right under the schema name.
	legacyIdx := strings.Index(body, "    LegacyBook:")
	if legacyIdx < 0 {
		t.Fatal("missing LegacyBook schema")
	}
	end := legacyIdx + 80
	if end > len(body) {
		end = len(body)
	}
	if !strings.Contains(body[legacyIdx:end], "deprecated: true") {
		t.Errorf("expected schema-level deprecated near LegacyBook:\n%s", body[legacyIdx:end])
	}

	// Field-level: the sku property carries its reason.
	if !strings.Contains(body, "use ISBN") {
		t.Errorf("expected field-level deprecation reason:\n%s", body)
	}

	// Method-level: only LegacyList is deprecated.
	legacyOpIdx := strings.Index(body, "/legacy:")
	newOpIdx := strings.Index(body, "/new:")
	if legacyOpIdx < 0 || newOpIdx < 0 {
		t.Fatal("missing operations")
	}
	legacyBlock := body[legacyOpIdx:newOpIdx]
	if !strings.Contains(legacyBlock, "deprecated: true") {
		t.Errorf("expected legacy operation deprecated:\n%s", legacyBlock)
	}
	if !strings.Contains(legacyBlock, "use NewList") {
		t.Errorf("expected method-level deprecation reason:\n%s", legacyBlock)
	}
	newBlock := body[newOpIdx:]
	if strings.Contains(newBlock[:200], "deprecated: true") {
		t.Errorf("non-deprecated operation should not be marked:\n%s", newBlock[:200])
	}
}

// A basePath variable is a variable of the document's server, which the field
// bound to it describes, and no operation's path parameter.
func TestBasePathVariablesAreServerVariables(t *testing.T) {
	const basePath = "/t/{tenant}/{region}"
	root, files := projectFiles(t, map[string]string{"a/a.craftgo": `package a
enum Region { eu  us }
type Req {
	// The tenant's slug.
	tenant string
	region Region
	id     string
}
type Resp { ok bool }
service S {
	get A /a/{id} { request Req  response Resp }
	post B /b { request Req  response Resp }
}`})
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root, BasePath: basePath})
	if len(diags) > 0 {
		t.Fatalf("semantic: %v", diags)
	}
	doc, err := buildProjectDocument(proj, &config.Config{OpenAPI: config.OpenAPI{BasePath: basePath}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(doc.Servers)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"url":"/t/{tenant}/{region}","variables":{"region":{"default":"eu","enum":["eu","us"]},"tenant":{"default":"tenant","description":"The tenant's slug."}}}]`
	if string(raw) != want {
		t.Errorf("servers = %s\nwant      %s", raw, want)
	}
	for path, params := range map[string][]string{"/a/{id}": {"id"}, "/b": nil} {
		var got []string
		for _, p := range doc.Paths.Find(path).Operations() {
			for _, ref := range p.Parameters {
				got = append(got, ref.Value.Name)
			}
		}
		if !slices.Equal(got, params) {
			t.Errorf("%s parameters = %v, want %v", path, got, params)
		}
	}
}

// Operations whose fields describe a basePath variable differently each get a
// server of their own, and a variable defaults to a value its field accepts.
func TestBasePathVariablesFollowEachOperation(t *testing.T) {
	const basePath = "/x/{tier}/{n}"
	root, files := projectFiles(t, map[string]string{
		"a/a.craftgo": `package a
enum Tier { gold  free }
type AReq { tier Tier  n int  name string }
type Ok { ok bool }
service S { post A /a { request AReq  response Ok } }`,
		"b/b.craftgo": `package b
type BReq {
	tier string @example("pro")
	n    int
}
type Ok2 { ok bool }
service T { get B /b { request BReq  response Ok2 } }`,
	})
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root, BasePath: basePath})
	if len(diags) > 0 {
		t.Fatalf("semantic: %v", diags)
	}
	doc, err := buildProjectDocument(proj, &config.Config{OpenAPI: config.OpenAPI{BasePath: basePath}})
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		servers any
		want    string
	}{
		"document": {doc.Servers, `[{"url":"/x/{tier}/{n}","variables":{"n":{"default":"0"},"tier":{"default":"tier"}}}]`},
		"A":        {doc.Paths.Find("/a").Post.Servers, `[{"url":"/x/{tier}/{n}","variables":{"n":{"default":"0"},"tier":{"default":"gold","enum":["gold","free"]}}}]`},
		"B":        {doc.Paths.Find("/b").Get.Servers, `[{"url":"/x/{tier}/{n}","variables":{"n":{"default":"0"},"tier":{"default":"pro"}}}]`},
	} {
		raw, err := json.Marshal(c.servers)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != c.want {
			t.Errorf("%s servers = %s\nwant %s", name, raw, c.want)
		}
	}
}

// A raw operation that binds no basePath variable takes any value there, so
// the document's server leaves the variable bare and a typed operation
// describing it gets a server of its own.
func TestBasePathVariablesOfARawOperationStayBare(t *testing.T) {
	const basePath = "/x/{tier}"
	root, files := projectFiles(t, map[string]string{"a/a.craftgo": `package a
enum Tier { gold  free }
type AReq { tier Tier  name string }
type Ok { ok bool }
service S {
	post A /a { request AReq  response Ok }
	@rawRequest
	post R /r { response Ok }
}`})
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root, BasePath: basePath})
	if len(diags) > 0 {
		t.Fatalf("semantic: %v", diags)
	}
	doc, err := buildProjectDocument(proj, &config.Config{OpenAPI: config.OpenAPI{BasePath: basePath}})
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		servers any
		want    string
	}{
		"document": {doc.Servers, `[{"url":"/x/{tier}","variables":{"tier":{"default":"tier"}}}]`},
		"A":        {doc.Paths.Find("/a").Post.Servers, `[{"url":"/x/{tier}","variables":{"tier":{"default":"gold","enum":["gold","free"]}}}]`},
		"R":        {doc.Paths.Find("/r").Post.Servers, `null`},
	} {
		raw, err := json.Marshal(c.servers)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != c.want {
			t.Errorf("%s servers = %s\nwant %s", name, raw, c.want)
		}
	}
}

// `basePath` goes into the server URL only, never into the path keys.
func TestGenerateOpenAPIBasePathNotDuplicated(t *testing.T) {
	pkg := analyze(t, `package design
type GetThingReq { id string @path }
@prefix("/v1")
service S {
    get GetThing /things/{id} { request GetThingReq }
}`)
	cfg := &config.Config{
		Package: "x/y",
		Output: config.Output{
			Types: "./internal/types", Transport: "./internal/transport",
			Routes: "./internal/routes", Service: "./internal/service",
			Svccontext: "./svccontext/svccontext.go",
			OpenAPI:    "./docs/openapi.yaml",
		},
		OpenAPI: config.OpenAPI{BasePath: "/api"},
	}
	root := t.TempDir()
	if err := genOpenAPI(t, pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	src := string(body)
	if !strings.Contains(src, "- url: /api") {
		t.Errorf("expected basePath in servers, got:\n%s", src)
	}
	if !strings.Contains(src, "/v1/things/{id}:") {
		t.Errorf("expected relative path key, got:\n%s", src)
	}
	if strings.Contains(src, "/api/v1/things/{id}:") {
		t.Errorf("basePath leaked into path key (regression):\n%s", src)
	}
}

func TestValidateSecuritySchemesHappyPath(t *testing.T) {
	cfg := &config.Config{
		Package: "x/y",
		OpenAPI: config.OpenAPI{
			SecuritySchemes: map[string]config.SecurityScheme{
				"bearerAuth": {Type: "http", Scheme: "bearer"},
			},
		},
	}
	if errs := validateSecuritySchemes(cfg); len(errs) != 0 {
		t.Errorf("expected no errors, got: %v", errs)
	}
}

func TestValidateSecuritySchemesOAuth2RequiresFlows(t *testing.T) {
	base := func(flows *config.OAuthFlows) *config.Config {
		return &config.Config{Package: "x/y", OpenAPI: config.OpenAPI{
			SecuritySchemes: map[string]config.SecurityScheme{
				"OAuth2": {Type: "oauth2", Flows: flows},
			},
		}}
	}
	// No flows → rejected.
	if errs := validateSecuritySchemes(base(nil)); len(errs) == 0 {
		t.Error("expected an error for an oauth2 scheme with no flows")
	}
	// With a flow → accepted.
	withFlow := &config.OAuthFlows{ClientCredentials: &config.OAuthFlow{
		TokenURL: "https://example.com/token",
		Scopes:   map[string]string{"read": "Read"},
	}}
	if errs := validateSecuritySchemes(base(withFlow)); len(errs) != 0 {
		t.Errorf("oauth2 with a flow should validate, got: %v", errs)
	}
	// The emitted scheme carries the flows object.
	sc := securitySchemeFor("OAuth2", base(withFlow))
	if sc.Flows == nil || sc.Flows.ClientCredentials == nil || sc.Flows.ClientCredentials.TokenURL == "" {
		t.Errorf("expected oauth2 flows emitted in the scheme, got %+v", sc.Flows)
	}
}

// Each oauth2 flow carries the URLs OpenAPI requires of its grant, and a
// message naming the scheme and the flow reports each one missing.
func TestValidateSecuritySchemesOAuth2FlowURLs(t *testing.T) {
	const auth, token = "https://auth.example.com/authorize", "https://auth.example.com/token"
	cfg := &config.Config{Package: "x/y", OpenAPI: config.OpenAPI{
		SecuritySchemes: map[string]config.SecurityScheme{
			"missing": {Type: "oauth2", Flows: &config.OAuthFlows{
				Implicit:          &config.OAuthFlow{TokenURL: token},
				Password:          &config.OAuthFlow{AuthorizationURL: auth},
				ClientCredentials: &config.OAuthFlow{RefreshURL: token},
				AuthorizationCode: &config.OAuthFlow{},
			}},
			"complete": {Type: "oauth2", Flows: &config.OAuthFlows{
				Implicit:          &config.OAuthFlow{AuthorizationURL: auth},
				Password:          &config.OAuthFlow{TokenURL: token},
				ClientCredentials: &config.OAuthFlow{TokenURL: token},
				AuthorizationCode: &config.OAuthFlow{AuthorizationURL: auth, TokenURL: token},
			}},
		},
	}}
	want := []string{
		`securityScheme "missing": flow implicit has no authorizationUrl`,
		`securityScheme "missing": flow password has no tokenUrl`,
		`securityScheme "missing": flow clientCredentials has no tokenUrl`,
		`securityScheme "missing": flow authorizationCode has no authorizationUrl`,
		`securityScheme "missing": flow authorizationCode has no tokenUrl`,
	}
	errs := validateSecuritySchemes(cfg)
	if len(errs) != len(want) {
		t.Fatalf("got %d messages, want %d:\n%s", len(errs), len(want), strings.Join(errs, "\n"))
	}
	for i, w := range want {
		if !strings.HasPrefix(errs[i], w) {
			t.Errorf("message %d = %q, want it to start %q", i, errs[i], w)
		}
	}
}

func TestGenerateOpenAPI(t *testing.T) {
	pkg := analyze(t, handlerSampleDSL)
	root := t.TempDir()
	cfg := sampleConfig()
	cfg.OpenAPI.Title = "API"
	cfg.OpenAPI.Version = "1.2.3"
	if err := genOpenAPI(t, pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(out)
	mustContainAll(t, src,
		"openapi: 3.1.0",
		"title: API",
		"version: 1.2.3",
		// The @prefix path; the basePath is the server URL.
		"/api/v1/users/{id}",
		"- url: /v1",
		"get:",
		"post:",
		"delete:",
		"operationId: GetUser",
		"#/components/schemas/User",
		// A type binding only parameters still gets a component.
		"GetUserReq:",
		"components:",
		"schemas:",
	)
	// No path key starts with the basePath.
	if strings.Contains(src, "/v1/api/v1/users/{id}") {
		t.Errorf("path key still has duplicated basePath:\n%s", src)
	}
}

func TestGenerateOpenAPIDefaultsAndEmpty(t *testing.T) {
	pkg := analyze(t, "package design")
	root := t.TempDir()
	cfg := sampleConfig()
	cfg.OpenAPI.Title = ""
	cfg.OpenAPI.Version = ""
	cfg.OpenAPI.BasePath = ""
	if err := genOpenAPI(t, pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	src := string(out)
	if !strings.Contains(src, "title: design") {
		t.Errorf("expected title fallback to package name:\n%s", src)
	}
	if !strings.Contains(src, "version: 0.1.0") {
		t.Errorf("expected default version:\n%s", src)
	}
}

func TestGenerateOpenAPITypeShapes(t *testing.T) {
	pkg := analyze(t, `package design
type Bag {
    items   string[]
    meta    map<string, string>
    age     int?
    name    string 
}`)
	root := t.TempDir()
	if err := genOpenAPI(t, pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	src := string(out)
	if !strings.Contains(src, "type: array") {
		t.Errorf("expected array type:\n%s", src)
	}
	if !strings.Contains(src, "additionalProperties") {
		t.Errorf("expected map → additionalProperties:\n%s", src)
	}
	if !strings.Contains(src, "- name") {
		t.Errorf("expected name in required list:\n%s", src)
	}
}

// A POST's @path and @query fields become parameters and the rest its body.
func TestGenerateOpenAPIPostWithQueryAndPath(t *testing.T) {
	pkg := analyze(t, `package design

type CreateReq {
    id       string  @path
    dryRun   bool    @query
    payload  string
}

type Resp { ok bool }

service S {
    post Create /things/{id} {
        request   CreateReq
        response  Resp
    }
}`)
	root := t.TempDir()
	if err := genOpenAPI(t, pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	src := string(out)
	mustContainAll(t, src,
		"requestBody:",
		"$ref: '#/components/schemas/CreateReqBody'",
		"CreateReqBody:",
		"CreateRespBody:",
		"$ref: '#/components/schemas/CreateRespBody'",
		"in: path",
		"in: query",
		"name: id",
		"name: dryRun",
	)
	// Parameters are inline, with no component of their own.
	mustContainNone(t, src, "CreateReqQuery:")
	// The unbound `payload` is body only.
	if strings.Contains(src, "name: payload") {
		t.Errorf("unmarked body field leaked into parameters:\n%s", src)
	}
}

// Path, query, header and cookie fields become inline parameters; only the
// body gets a component.
func TestGenerateOpenAPICookieAndHeaderInline(t *testing.T) {
	pkg := analyze(t, `package design

type CallReq {
    id        string  @path
    dryRun    bool    @query
    apiKey    string  @header
    session   string  @cookie
    payload   string
}

type Resp { ok bool }

service S {
    post Call /things/{id} {
        request   CallReq
        response  Resp
    }
}`)
	root := t.TempDir()
	if err := genOpenAPI(t, pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	src := string(out)
	mustContainAll(t, src,
		"CallReqBody:",
		"in: query",
		"name: dryRun",
		"in: header",
		"name: apiKey",
		"in: cookie",
		"name: session",
		"in: path",
	)
	mustContainNone(t, src, "CallReqQuery:", "CallReqHeader:", "CallReqCookie:", "CallReqPath:")
}

// Service and method `@tags`, string or identifier, combine; a service with
// none tags its operations with its name.
func TestGenerateOpenAPITagsFromDecorators(t *testing.T) {
	pkg := analyze(t, `package design

type R { ok bool }

@tags(admin, ops)
service S {
    @tags(snapshot)
    get One /one {
        response R
    }

    @tags("v2")
    get Two /two {
        response R
    }

    get Three /three {
        response R
    }
}

service Bare {
    get B /b {
        response R
    }
}`)
	root := t.TempDir()
	if err := genOpenAPI(t, pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	src := string(out)
	mustContainAll(t, src,
		// One inherits service tags + adds its own.
		"operationId: One",
		"- admin",
		"- ops",
		"- snapshot",
		// Two also inherits and adds a string-literal tag.
		"operationId: Two",
		"- v2",
		// Bare service has no @tags → defaults to the service name.
		"operationId: B",
		"- Bare",
	)
}

// A service's @group is a tag after its @tags, deduplicated; `@ignoreTags`
// drops it with the rest of the service's tags.
func TestGenerateOpenAPIGroupAddsTag(t *testing.T) {
	pkg := analyze(t, `package design

type R { id string @path }
type Resp { ok bool }

@prefix("/v1")
@group("admin/ops")
@tags(users)
service S {
    get Append /a/{id} { request R  response Resp }

    @ignoreTags
    get Drop /b/{id} { request R  response Resp }
}

@prefix("/v2")
@group("billing")
@tags(billing)
service T {
    get Dedup /c/{id} { request R  response Resp }
}`)
	root := t.TempDir()
	if err := genOpenAPI(t, pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	src := string(out)
	mustContainAll(t, src,
		// Explicit @tags first, then the group value appended.
		"operationId: Append",
		"- users",
		"- admin/ops",
		// Drop has no tag left, so it gets the service name.
		"operationId: Drop",
		"- S",
	)
	// Group value equal to an explicit tag is deduped to a single entry.
	if n := strings.Count(src, "- billing"); n != 1 {
		t.Errorf("@group(\"billing\") + @tags(billing) should dedup to one tag, got %d occurrences:\n%s", n, src)
	}
}

// `@format(datetime)` is written `date-time`; `date` stays `date`.
func TestGenerateOpenAPIDatetimeFormatKeyword(t *testing.T) {
	pkg := analyze(t, `package design

type R {
    created string @format(datetime)
    born    string @format(date)
}

service S {
    get Get /a { response R }
}`)
	root := t.TempDir()
	if err := genOpenAPI(t, pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	src := string(out)
	if strings.Contains(src, "format: datetime") {
		t.Errorf("expected non-standard 'format: datetime' to be remapped to 'date-time', got:\n%s", src)
	}
	mustContainAll(t, src,
		"format: date-time",
		"format: date",
	)
}

// int32, int64, float32 and float64 carry their standard format and the other
// widths none; an unsigned type has `minimum: 0`, which `@gte` tightens.
func TestGenerateOpenAPINumericFormats(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
type R {
    i8  int8
    i16 int16
    i32 int32
    i64 int64
    ii  int
    f32 float32
    f64 float64
    u32 uint32
    ug  uint32 @gte(5)
}
service S { get Get /a { response R } }`)

	// block returns the lines under `<field>:`, up to the next sibling.
	block := func(field string) string {
		lines := strings.Split(body, "\n")
		start, indent := -1, 0
		for i, ln := range lines {
			t := strings.TrimSpace(ln)
			if t == field+":" {
				start = i
				indent = len(ln) - len(strings.TrimLeft(ln, " "))
				break
			}
		}
		if start < 0 {
			t.Fatalf("field %q not found in:\n%s", field, body)
		}
		var out []string
		for _, ln := range lines[start+1:] {
			if strings.TrimSpace(ln) == "" {
				out = append(out, ln)
				continue
			}
			if len(ln)-len(strings.TrimLeft(ln, " ")) <= indent {
				break // back to sibling / parent indent
			}
			out = append(out, ln)
		}
		return strings.Join(out, "\n")
	}
	want := map[string]string{
		"i32": "format: int32",
		"i64": "format: int64",
		"f32": "format: float",
		"f64": "format: double",
		"u32": "minimum: 0",
		"ug":  "minimum: 5", // user @gte(5) tightens the unsigned floor
	}
	for f, sub := range want {
		if b := block(f); !strings.Contains(b, sub) {
			t.Errorf("field %s: expected %q in:\n%s", f, sub, b)
		}
	}
	// int, int8 and int16 have no standard format.
	for _, f := range []string{"ii", "i8", "i16"} {
		if b := block(f); strings.Contains(b, "format:") {
			t.Errorf("field %s should carry no format (no standard keyword), got:\n%s", f, b)
		}
	}
}

// The operationId is the method name, or `@operationId`'s string verbatim.
func TestGenerateOpenAPIOperationIDDefaultAndOverride(t *testing.T) {
	pkg := analyze(t, `package design

type R { ok bool }

service S {
    // No decorator → defaults to PascalCase method name.
    get DefaultID /a {
        response R
    }

    // Decorator override - exact verbatim string.
    @operationId("custom-kebab-id")
    get OverrideID /b {
        response R
    }
}`)
	root := t.TempDir()
	if err := genOpenAPI(t, pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	src := string(out)
	if !strings.Contains(src, "operationId: DefaultID") {
		t.Errorf("expected default PascalCase operationId, got:\n%s", src)
	}
	if !strings.Contains(src, "operationId: custom-kebab-id") {
		t.Errorf("expected @operationId override, got:\n%s", src)
	}
}

// A tag keeps its spaces.
func TestGenerateOpenAPITagsWithSpaces(t *testing.T) {
	pkg := analyze(t, `package design

type R { ok bool }

@tags("user management", "v1")
service S {
    get One /one {
        response R
    }
}`)
	root := t.TempDir()
	if err := genOpenAPI(t, pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	src := string(out)
	mustContainAll(t, src,
		`- user management`,
		`- v1`,
	)
}

// A passthrough response documents `*/*` and a file upload
// `multipart/form-data`.
func TestGenerateOpenAPIPerModeMediaTypes(t *testing.T) {
	const dsl = `package design

type UploadReq { note string  avatar file }
type UploadResp { ok bool }

service S {
    @passthrough
    get LiveTail /tail {
    }
    post Upload /upload {
        request   UploadReq
        response  UploadResp
    }
}`
	pkg := analyze(t, dsl)
	root := t.TempDir()
	if err := genOpenAPI(t, pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(out)
	mustContainAll(t, src,
		"'*/*'",
		"multipart/form-data",
		"format: binary",
	)
	// The Upload operation's own body is multipart.
	uploadIdx := strings.Index(src, "operationId: Upload")
	if uploadIdx < 0 {
		t.Fatalf("Upload operation not found in spec")
	}
	uploadBlock := src[uploadIdx:]
	if end := strings.Index(uploadBlock[1:], "operationId:"); end >= 0 {
		uploadBlock = uploadBlock[:end+1]
	}
	if !strings.Contains(uploadBlock, "multipart/form-data") {
		t.Errorf("Upload op missing multipart/form-data:\n%s", uploadBlock)
	}
}

// `@security([A, B])` registers both schemes.
func TestGenerateOpenAPISecurityArrayRegistersSchemes(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
type Req { id string }
service S {
    @security([Bearer, ApiKey])
    get Get /x { request Req }
}`)
	mustContainAll(t, body, "Bearer:", "ApiKey:")
}

// An error schema requires its non-optional fields.
func TestGenerateOpenAPIErrorSchemaRequired(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
error Conflict DuplicateKey { resource string  detail string? }
type Req { id string }
type Res { ok bool }
service S {
    @errors(DuplicateKey)
    post Make /m { request Req  response Res }
}`)
	i := strings.Index(body, "DuplicateKeyErr:")
	if i < 0 {
		t.Fatalf("error schema missing:\n%s", body)
	}
	block := body[i:min(i+300, len(body))]
	r := strings.Index(block, "required:")
	if r < 0 || !strings.Contains(block[r:], "- resource") {
		t.Errorf("required[] must list non-optional `resource`:\n%s", block)
	}
	if r >= 0 && strings.Contains(block[r:min(r+50, len(block))], "detail") {
		t.Errorf("optional `detail` must not be in required[]:\n%s", block)
	}
}

// `@minItems` and `@maxItems` on a map become minProperties and maxProperties.
func TestGenerateOpenAPIMapItemsConstraints(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
type T { counts map<string, int> @minItems(1) @maxItems(50) }
service S { post Make /m { request T } }`)
	mustContainAll(t, body, "minProperties: 1", "maxProperties: 50")
	if strings.Contains(body, "minItems") || strings.Contains(body, "maxItems") {
		t.Errorf("map size must use min/maxProperties, not min/maxItems:\n%s", body)
	}
}

// `Page<Email>`'s items $ref the Email scalar component.
func TestGenerateOpenAPIGenericScalarArg(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
scalar Email string @format(email)
type Page<T> { items T[] }
type EmailList { p Page<Email> }
service S { get Get /e { response EmailList } }`)
	mustContainAll(t, body, "PageOfEmail:", "$ref: '#/components/schemas/Email'")
}

// A multipart request's inline body gets no `<base>ReqBody` component.
func TestGenerateOpenAPIMultipartNoOrphanReqBody(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
type UploadReq { doc file  title string @form }
type Resp { ok bool }
@prefix("/u")
service S { post Upload /up { request UploadReq  response Resp } }`)
	if strings.Contains(body, "UploadReqBody") {
		t.Errorf("multipart request emitted an orphaned UploadReqBody component:\n%s", body)
	}
	mustContainAll(t, body, "multipart/form-data")
}

// genOpenAPI writes pkg's OpenAPI document as a single-package project.
func genOpenAPI(t *testing.T, pkg *semantic.Package, cfg *config.Config, root string) error {
	t.Helper()
	proj := &semantic.Project{Packages: map[string]*semantic.Package{pkg.Name: pkg}}
	return GenerateOpenAPI(proj, cfg, root)
}

// `@doc` on a named-type field wraps its $ref in an allOf that carries the
// description.
func TestNamedRefDocExampleWrappedInAllOf(t *testing.T) {
	root, files := projectFiles(t, map[string]string{
		"m/m.craftgo": `package m
type Inner { x int }
type Outer {
  child Inner @doc("the inner child") @example("ignored-by-object")
}`,
	})
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("semantic: %v", diags)
	}
	doc, err := buildProjectDocument(proj, &config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	outer := doc.Components.Schemas["Outer"]
	if outer == nil || outer.Value == nil {
		t.Fatal("no Outer schema")
	}
	child := outer.Value.Properties["child"]
	if child == nil || child.Value == nil {
		t.Fatal("no child property")
	}
	if child.Ref != "" {
		t.Fatalf("child stayed a bare $ref; @doc dropped: %q", child.Ref)
	}
	if len(child.Value.AllOf) != 1 || child.Value.AllOf[0].Ref == "" {
		t.Fatalf("child not wrapped in allOf:[{$ref}]: %+v", child.Value)
	}
	if child.Value.Description != "the inner child" {
		t.Fatalf("@doc not carried onto the wrapper: %q", child.Value.Description)
	}
}

// genDoc builds the merged document of the design in src.
func genDoc(t *testing.T, src map[string]string, cfg *config.Config) *openapi3.T {
	t.Helper()
	root, files := projectFiles(t, src)
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("semantic: %v", diags)
	}
	doc, err := buildProjectDocument(proj, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// `@minItems` gives a map field minProperties and an optional named-type
// field's wrapper nothing.
func TestMinItemsNotLeakedOntoNamedTypeWrapper(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"m/m.craftgo": `package m
type Inner { a string }
type T {
  x Inner? @minItems(2)
  mp map<string, int> @minItems(2)
}`,
	}, &config.Config{})
	props := doc.Components.Schemas["T"].Value.Properties
	if got := props["x"].Value.MinProps; got != 0 {
		t.Errorf("struct field wrapper leaked minProperties=%d (want 0)", got)
	}
	if got := props["mp"].Value.MinProps; got != 2 {
		t.Errorf("map field should keep minProperties=2, got %d", got)
	}
}

// `@errors([...])` and `@tags([...])` add their responses and tags.
func TestArrayShortcutErrorsAndTags(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"s/s.craftgo": `package s
error NotFound Missing { resource string }
type Out { ok bool }
service S {
  @errors([Missing])
  @tags([alpha, beta])
  get Arr /arr { response Out }
}`,
	}, &config.Config{})
	op := doc.Paths.Find("/arr").Get
	if op.Responses.Status(404) == nil {
		t.Error("@errors([Missing]) dropped the 404 response")
	}
	if len(op.Tags) != 2 || op.Tags[0] != "alpha" || op.Tags[1] != "beta" {
		t.Errorf("@tags([alpha, beta]) not applied, got: %v", op.Tags)
	}
}

// Stacked exclusive bounds document the tightest one, in either order.
func TestExclusiveBoundsIntersect(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"ct/ct.craftgo": `package ct
type T {
  a int @gt(5) @positive
  b int @positive @gt(5)
  c int @lt(-5) @negative
}
service S { post M /m { request T  response T } }`,
	}, &config.Config{})
	s := doc.Components.Schemas["T"].Value
	for _, f := range []string{"a", "b"} {
		got := s.Properties[f].Value.Extensions["exclusiveMinimum"]
		if got != float64(5) {
			t.Errorf("field %s exclusiveMinimum = %v, want 5 (tightest)", f, got)
		}
	}
	if got := s.Properties["c"].Value.Extensions["exclusiveMaximum"]; got != float64(-5) {
		t.Errorf("field c exclusiveMaximum = %v, want -5 (tightest)", got)
	}
}

// An integer bound a float64 cannot hold, written as an integer or a whole
// float, is documented as that exact integer, the tighter of two such bounds
// too.
func TestIntegerBoundsAreExact(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
type Limits {
	a int64  @range(-9223372036854775808.0, 9223372036854775807.0) @multipleOf(9223372036854775807.0)
	b int64  @gt(-9223372036854775807) @lt(9223372036854775807)
	c uint64 @multipleOf(18446744073709551615.0) @gte(9007199254740993)
	d int64  @lte(9223372036854775000) @range(0, 9223372036854775807)
	e string @maxLength(9223372036854775807)
}
service S { post M /m { request Limits  response Limits } }`)
	mustContainAll(t, body,
		"minimum: -9223372036854775808",
		"maximum: 9223372036854775807",
		"multipleOf: 9223372036854775807",
		"exclusiveMinimum: -9223372036854775807",
		"exclusiveMaximum: 9223372036854775807",
		"multipleOf: 18446744073709551615",
		"minimum: 9007199254740993",
		"maximum: 9223372036854775000",
		"maxLength: 9223372036854775807",
	)
	mustContainNone(t, body, "e+18", "e+19", "9223372036854776000", "maxLength: 9223372036854775808")
}

// A float bound judges the literal and the float the validator compares
// against as the validator does; an integer field keeps the literal.
func TestFloatBoundsAgreeWithTheValidator(t *testing.T) {
	doc := genDoc(t, map[string]string{"a/a.craftgo": `package a
scalar P32 float32
scalar P64 float64 @lte(9223372036854775807.0)
type Limits {
	a float64 @gte(9007199254740993.0) @lte(18446744073709551615.0)
	b float64 @lt(9007199254740993)
	c float32 @range(16777217, 16777219.0)
	d float32 @gt(0.1)
	e P32 @lte(16777217)
	f P32? @lt(0.1)
	g int64 @lte(9223372036854775807.0)
	h float32? @gte(0.1) @default(0.1)
	i float32 @lte(0.7) @example(0.7)
	j float64 @lte(9007199254740993)
	k float32 @lt(0.7)
	l float32 @gte(1.0000000596046447)
}
service S { post M /m { request Limits  response Limits } }`}, &config.Config{})
	keywords := func(ref *openapi3.SchemaRef) map[string]any {
		raw, err := json.Marshal(ref)
		if err != nil {
			t.Fatal(err)
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var out map[string]any
		if err := dec.Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	props := doc.Components.Schemas["Limits"].Value.Properties
	for name, c := range map[string]struct {
		schema *openapi3.SchemaRef
		want   map[string]string
	}{
		"a":   {props["a"], map[string]string{"minimum": "9007199254740992", "maximum": "18446744073709551616"}},
		"b":   {props["b"], map[string]string{"exclusiveMaximum": "9007199254740992"}},
		"c":   {props["c"], map[string]string{"minimum": "16777216", "maximum": "16777220"}},
		"d":   {props["d"], map[string]string{"exclusiveMinimum": "0.10000000149011612"}},
		"e":   {props["e"].Value.AllOf[1], map[string]string{"maximum": "16777217"}},
		"f":   {props["f"], map[string]string{"exclusiveMaximum": "0.1"}},
		"g":   {props["g"], map[string]string{"maximum": "9223372036854775807"}},
		"h":   {props["h"], map[string]string{"minimum": "0.1", "default": "0.1"}},
		"i":   {props["i"], map[string]string{"maximum": "0.7", "example": "0.7"}},
		"j":   {props["j"], map[string]string{"maximum": "9007199254740993"}},
		"k":   {props["k"], map[string]string{"exclusiveMaximum": "0.699999988079071"}},
		"l":   {props["l"], map[string]string{"minimum": "1.0000001192092896"}},
		"P64": {doc.Components.Schemas["P64"], map[string]string{"maximum": "9223372036854775808"}},
	} {
		got := keywords(c.schema)
		for kw, want := range c.want {
			if n, _ := got[kw].(json.Number); string(n) != want {
				t.Errorf("%s.%s = %v, want %s", name, kw, got[kw], want)
			}
		}
	}
}

// A bodyless error's schema is the `{code, message}` envelope the runtime
// sends.
func TestBodylessErrorEnvelopeSchema(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"app/app.craftgo": `package app
error NotFound RecordNotFound
type Req { id string @path }
type Item { id string }
service App {
  @errors(RecordNotFound)
  get One /app/{id} { request Req  response Item }
}`,
	}, &config.Config{})
	s := doc.Components.Schemas["RecordNotFoundErr"].Value
	if s.Properties["code"] == nil || s.Properties["message"] == nil {
		t.Errorf("bodyless error schema missing code/message envelope: %+v", s.Properties)
	}
}

// An error whose fields, a mixin's included, all ride a header, a cookie or
// nowhere documents the `{code, message}` envelope it is written as.
func TestErrorWithoutJSONMemberEnvelopeSchema(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"app/app.craftgo": `package app
type Wait { seconds int @header("X-Wait") }
error TooManyRequests RateLimited { retryAfter int @header("Retry-After") }
error Unauthorized Expired { session string @cookie("sid") }
error BadRequest Secretive { internal string @sensitive }
error ServiceUnavailable Busy { Wait }
type Req { id string @path }
type Item { id string }
service App {
  @errors(RateLimited, Expired, Secretive, Busy)
  get One /app/{id} { request Req  response Item }
}`,
	}, &config.Config{})
	for _, name := range []string{"RateLimitedErr", "ExpiredErr", "SecretiveErr", "BusyErr"} {
		s := doc.Components.Schemas[name].Value
		if len(s.AllOf) > 0 || s.Properties["code"] == nil || s.Properties["message"] == nil ||
			!slices.Equal(s.Required, []string{"code", "message"}) {
			t.Errorf("%s: want the {code, message} envelope, got allOf=%d properties=%v required=%v",
				name, len(s.AllOf), slices.Sorted(maps.Keys(s.Properties)), s.Required)
		}
	}
	busy := doc.Paths.Find("/app/{id}").Get.Responses.Value("503").Value
	if busy.Headers["X-Wait"] == nil {
		t.Errorf("503 response lost the mixin's X-Wait header: %v", slices.Sorted(maps.Keys(busy.Headers)))
	}
}

// Two operations of one method on one OpenAPI path, the exact root `/{$}` and
// the root `/`, stop the document with an error naming both.
func TestOperationsSharingAnOpenAPIPathRefused(t *testing.T) {
	root, files := projectFiles(t, map[string]string{
		"app/app.craftgo": `package app
type Resp { ok bool }
@prefix("/{$}")
service S { get A / { response Resp } }
service T { get B / { response Resp } }`,
	})
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("semantic: %v", diags)
	}
	_, err := buildProjectDocument(proj, &config.Config{})
	if err == nil || !strings.Contains(err.Error(), `OpenAPI path GET / holds two operations: S.A (GET /{$}) and T.B (GET /)`) {
		t.Fatalf("want the shared path refused, got %v", err)
	}
}

// A trailing {name...} variable is a string path parameter named name, which
// the path template names; a raw operation declares it too.
func TestRestVariableDocumentedAsItsName(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"app/app.craftgo": `package app
type FileReq { rest string }
type Resp { ok bool }
@prefix("/files/{rest...}")
service S {
  get Get / { request FileReq  response Resp }
  @rawRequest
  put Put / { response Resp }
}`,
	}, &config.Config{})
	item := doc.Paths.Value("/files/{rest}")
	if item == nil {
		t.Fatalf("no /files/{rest} path, got %v", doc.Paths.InMatchingOrder())
	}
	for verb, op := range map[string]*openapi3.Operation{"get": item.Get, "put": item.Put} {
		if op == nil || len(op.Parameters) != 1 {
			t.Fatalf("%s: want one parameter, got %+v", verb, op)
		}
		p := op.Parameters[0].Value
		if p.Name != "rest" || p.In != "path" || !p.Schema.Value.Type.Is("string") {
			t.Errorf("%s: parameter %s in %s, want the string path parameter rest", verb, p.Name, p.In)
		}
	}
}

// A scheme the manifest declares is emitted as declared.
func TestSecuritySchemeFromConfig(t *testing.T) {
	cfg := &config.Config{}
	cfg.OpenAPI.SecuritySchemes = map[string]config.SecurityScheme{
		"MFA": {Type: "apiKey", In: "header", Name: "X-MFA-Token"},
	}
	doc := genDoc(t, map[string]string{
		"s/s.craftgo": `package s
type Out { ok bool }
service S {
  @security(MFA)
  get A /a { response Out }
}`,
	}, cfg)
	scheme := doc.Components.SecuritySchemes["MFA"]
	if scheme == nil || scheme.Value == nil {
		t.Fatal("MFA scheme not registered")
	}
	if scheme.Value.Type != "apiKey" || scheme.Value.In != "header" || scheme.Value.Name != "X-MFA-Token" {
		t.Errorf("MFA scheme not emitted from config: type=%q in=%q name=%q", scheme.Value.Type, scheme.Value.In, scheme.Value.Name)
	}
}

// `@example(Green)` on an enum field documents the member's wire value.
func TestExampleEnumMemberResolved(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"m/m.craftgo": `package m
enum Color { Red Green Blue }
type Thing { c Color @example(Green) }
type Req { id string @path }
service Svc { get Fetch /t/{id} { request Req  response Thing } }`,
	}, &config.Config{})
	thing := doc.Components.Schemas["Thing"].Value
	c := thing.Properties["c"]
	if c == nil || c.Value == nil || c.Value.Example == nil {
		t.Fatalf("@example(Green) dropped from enum field: %+v", c)
	}
	if c.Value.Example != "Green" {
		t.Errorf("@example(Green) = %v, want wire value \"Green\"", c.Value.Example)
	}
}

// A merge rename onto another declaration's name (`shared.User` to the
// `SharedUser` of package api) is a collision.
func TestCrossPkgMergeNameCollisionRejected(t *testing.T) {
	root, files := projectFiles(t, map[string]string{
		"shared/s.craftgo": `package shared
type User { a string }`,
		"api/a.craftgo": `package api
import "shared"
type User { b int }
type SharedUser { c bool }
type Resp { y shared.User  z SharedUser }
type Req { ok bool }
service S { get Do /do { request Req  response Resp } }`,
	})
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("semantic: %v", diags)
	}
	if dups := projectMergeCollisions(proj); len(dups) == 0 {
		t.Error("expected cross-pkg merge name collision (shared.User vs api.SharedUser)")
	}
}

func TestGenerateOpenAPIJSONKeyOverride(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
type Item { id string }
type Req { items Item[] @json("OrderItem")  storeId string @json("store_id") }
service S {
    post Make /m { request Req }
}`)
	for _, want := range []string{"OrderItem:", "store_id:", "- OrderItem", "- store_id"} {
		if !strings.Contains(body, want) {
			t.Errorf("the document does not carry the @json key %s:\n%s", want, body)
		}
	}
	for _, stale := range []string{"storeId:", "- storeId", "- items"} {
		if strings.Contains(body, stale) {
			t.Errorf("the document still names the field %s instead of its @json key:\n%s", stale, body)
		}
	}
}

// A `bytes @format(raw)` field has an untyped schema with only the raw note;
// `any` is untyped without it, and plain `bytes` stays `format: byte`.
func TestGenerateOpenAPIRawBytesIsUnconstrained(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
type Req { payload bytes @format(raw)  meta bytes? @format(raw)  raw any  blob bytes }
service S {
    post Make /m { request Req }
}`)
	for _, want := range []string{
		"payload:\n          description: raw encoded value",
		"meta:\n          description: raw encoded value",
		"raw: {}",
		"format: byte",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the document does not carry %q:\n%s", want, body)
		}
	}
	// A raw field has no type.
	if strings.Contains(body, "raw encoded value\n          type:") {
		t.Errorf("a raw field must not be given a type:\n%s", body)
	}
}

// A raw-bytes scalar's doc goes ahead of the raw note.
func TestGenerateOpenAPIRawBytesScalar(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
// A document stored as it arrived.
scalar RawDoc bytes @format(raw)
type Req { photos RawDoc }
service S {
    post Make /m { request Req }
}`)
	if !strings.Contains(body, "A document stored as it arrived.\n\n        raw encoded value") {
		t.Errorf("the scalar schema does not carry its doc ahead of the raw note:\n%s", body)
	}
}

// A scalar or enum response gets a `<base>RespBody` that $refs its
// component, and every $ref in the document resolves.
func TestGenerateOpenAPIScalarAndEnumResponseRefsResolve(t *testing.T) {
	for label, src := range map[string]string{
		"scalar": "package design\nscalar Token string\ntype Req { v string }\nservice S { post Do /do { request Req  response Token } }",
		"enum":   "package design\nenum Color { red green }\ntype Req { v string }\nservice S { post Do /do { request Req  response Color } }",
	} {
		t.Run(label, func(t *testing.T) {
			body := generateOpenAPIToString(t, src)
			declared := declaredSchemaNames(body)
			for _, ref := range schemaRefNames(body) {
				if !declared[ref] {
					t.Errorf("$ref %q has no component schema; declared: %v\n%s", ref, declared, body)
				}
			}
			if !declared["DoRespBody"] {
				t.Errorf("DoRespBody component missing:\n%s", body)
			}
		})
	}
}

// declaredSchemaNames returns the keys under `components.schemas`, written
// at a four-space indent.
func declaredSchemaNames(body string) map[string]bool {
	out := map[string]bool{}
	inSchemas := false
	for _, line := range strings.Split(body, "\n") {
		switch {
		case line == "  schemas:":
			inSchemas = true
		case inSchemas && strings.HasPrefix(line, "    ") && !strings.HasPrefix(line, "     ") && strings.HasSuffix(line, ":"):
			out[strings.TrimSuffix(strings.TrimSpace(line), ":")] = true
		case inSchemas && line != "" && !strings.HasPrefix(line, "    "):
			inSchemas = false
		}
	}
	return out
}

// schemaRefNames returns every component name the document $refs.
func schemaRefNames(body string) []string {
	const marker = "#/components/schemas/"
	var out []string
	for rest := body; ; {
		i := strings.Index(rest, marker)
		if i < 0 {
			return out
		}
		rest = rest[i+len(marker):]
		end := strings.IndexAny(rest, "'\" \n")
		if end < 0 {
			end = len(rest)
		}
		out = append(out, rest[:end])
	}
}
