package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// ---------- enum / scalar schema emission ----------

// generateOpenAPIToString runs the OpenAPI generator on `src` and
// returns the resulting YAML body as a string. Wraps the boilerplate
// (analyze → temp dir → ReadFile) so each golden-driven openapi
// test reads as just `src` + `expectGolden`.
func generateOpenAPIToString(t *testing.T, src string) string {
	t.Helper()
	pkg := analyze(t, src)
	root := t.TempDir()
	if err := GenerateOpenAPI(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(root, "docs", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// TestGenerateOpenAPIEnumSchemasEmitted pins the bug fix: every field
// referencing an enum type produces a `$ref` whose target schema
// lives under components.schemas. Both string-based and int-based
// enums are exercised. The full YAML goes through a golden snapshot;
// regressions surface as a diff hunk pointing at the divergent line.
// TestGenerateOpenAPISensitiveFieldOmitted asserts that `@sensitive`
// fields are skipped entirely from the OpenAPI spec - not present in
// schema.properties, not listed under required, not surfaced as a
// query / path / header / cookie parameter (sensitive can't combine
// with binding decorators, but defensive double-check).
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

// TestGenerateOpenAPIScalarSchemasEmitted covers the parallel fix for
// scalar declarations.
func TestGenerateOpenAPIScalarSchemasEmitted(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
scalar Email string @format(email)
type Req { addr Email }
service S { post Send /m { request Req } }`)
	expectGolden(t, "openapi-scalar-schemas.yaml", body)
}

// TestGenerateOpenAPIScalarFullConstraints checks that every scalar
// decorator family (format / length / pattern / numeric bounds /
// multipleOf) flows into the component schema. If only `@format`
// reached OpenAPI, generated TS clients would lose validators the
// runtime still enforces — a silent spec/runtime drift.
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
		// Step
		"exclusiveMinimum: true",
		"multipleOf: 5",
	)
}

// TestGenerateOpenAPIErrorsDecorator pins the @errors flow:
// referenced error decls land as components.schemas entries AND as
// per-operation responses keyed by the error category's HTTP status.
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
    @status(201)
    post CreateBook /books { request Book  response Book }
}`
	pkg := analyze(t, src)
	root := t.TempDir()
	if err := GenerateOpenAPI(pkg, sampleConfig(), root); err != nil {
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
	// CreateBook overrides success to 201 via @status.
	if !strings.Contains(body, `"201":`) {
		t.Errorf("expected @status(201) override:\n%s", body)
	}
	// 201 response carries the IANA reason phrase, not the hardcoded "OK".
	if !strings.Contains(body, `description: Created`) {
		t.Errorf("expected `description: Created` for 201 response:\n%s", body)
	}
	// CreateBook also registers 409 (Conflict) for DuplicateISBN.
	if !strings.Contains(body, `"409":`) {
		t.Errorf("expected 409 Conflict response:\n%s", body)
	}
}

// TestGenerateOpenAPISameStatusErrorsMerge pins the @errors merge:
// two error decls sharing one HTTP status (e.g. both Conflict) must
// render as `oneOf` so neither vanishes from the spec. Before the
// merge, the second op.Responses.Set call overwrote the first and the
// earlier error became invisible to OpenAPI consumers.
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
	if err := GenerateOpenAPI(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	body := string(out)
	if !strings.Contains(body, "oneOf:") {
		t.Errorf("expected oneOf for same-status errors:\n%s", body)
	}
	// Count $ref entries between `oneOf:` and the next non-list-item
	// line so the assertion fails fast if codegen accidentally
	// duplicates or drops a schema (e.g. a deduplication bug would
	// silently let the test pass on substring presence alone).
	oneOfIdx := strings.Index(body, "oneOf:")
	if oneOfIdx < 0 {
		t.Fatalf("oneOf block missing:\n%s", body)
	}
	tail := body[oneOfIdx:]
	if end := strings.Index(tail, "\n            description:"); end > 0 {
		tail = tail[:end]
	}
	refCount := strings.Count(tail, "$ref:")
	if refCount != 2 {
		t.Errorf("oneOf must list exactly 2 $refs (one per declared error), got %d:\n%s", refCount, tail)
	}
	mustContainAll(t, body,
		"EmailTakenErr",
	)
}

// TestGenerateOpenAPIDocSummaryDescription covers the doc-flavour
// trio: type/field/operation `@doc` propagates to OpenAPI
// `description`, `@summary` lands on the operation summary, and
// leading `// comment` blocks fall through as descriptions when no
// `@doc` is supplied.
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

// TestGenerateOpenAPIExampleNullable covers the field-side metadata
// pair (@example/@nullable). Aliases on enum values were removed -
// the API surface insisted on canonical wire vocabulary.
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
	if err := GenerateOpenAPI(pkg, sampleConfig(), root); err != nil {
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
	if !strings.Contains(src2, "nullable: true") {
		t.Errorf("expected nullable: true on field:\n%s", src2)
	}
}

// TestGenerateOpenAPIValidatorConstraints pins the mapping from
// validator decorators onto OpenAPI's numeric / string / array /
// pattern / format keywords. Without this wiring, client generators
// would see fields as unbounded primitives and produce types that
// don't match the server's accepted shape.
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
		"exclusiveMinimum: true",
		"exclusiveMaximum: true",
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

// TestGenerateOpenAPIMultipartMimeTypes checks that a `file @form`
// field with `@mimeTypes(...)` surfaces the allowlist under
// multipart/form-data `encoding[field].contentType` so generated
// client SDKs can pre-check / warn the user when the upload's MIME
// falls outside the contract. Without the encoding entry, only the
// runtime validator carries the allowlist — client SDKs upload blind.
func TestGenerateOpenAPIMultipartMimeTypes(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
type UploadReq {
    userId string @path
    avatar file   @form @maxSize(2MB) @mimeTypes("image/png", "image/jpeg")
    doc    file   @form
}
service S {
    post Upload /users/{userId}/avatar { request UploadReq  response UploadReq }
}`)
	mustContainAll(t, body,
		"multipart/form-data:",
		"format: binary",
		"encoding:",
		"avatar:",
		"contentType: image/png, image/jpeg",
	)
	// File without @mimeTypes leaves encoding empty for that field.
	if strings.Contains(body, "doc:\n          contentType") {
		t.Errorf("file without @mimeTypes should not produce contentType:\n%s", body)
	}
}

// TestGenerateOpenAPIMixinFlatten checks that embedded mixins
// surface in the host schema via OpenAPI's allOf composition so
// generated TS clients see every field — including those inherited
// from the mixin. Without it, runtime requests carrying mixin
// fields (`createdAt`, `updatedAt`, ...) fail type-checks against
// the spec because the host schema lists only its own properties.
func TestGenerateOpenAPIMixinFlatten(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
type Audit { createdAt string @format(datetime)  updatedAt string @format(datetime) }
type User { Audit  id string  name string }
service S { post Create /c { request User  response User } }`)
	// User schema must use allOf with Audit ref + its own properties.
	if !strings.Contains(body, "allOf:") {
		t.Errorf("mixin host should use allOf:\n%s", body)
	}
	if !strings.Contains(body, "$ref: '#/components/schemas/Audit'") {
		t.Errorf("mixin ref missing:\n%s", body)
	}
	// Host's own properties must still appear via the inline branch.
	if !strings.Contains(body, "id:") || !strings.Contains(body, "name:") {
		t.Errorf("host properties missing:\n%s", body)
	}
}

// TestGenerateOpenAPIGenericInstanceEmitsComponent pins the core
// generic-naming contract: every distinct generic instantiation lands
// as its own component in `components.schemas`, and the reference
// site emits a `$ref` instead of inlining the body. Without per-
// instantiation components, OpenAPI consumers would see anonymous
// inline schemas and fail to discriminate Page<Order> from Page<User>.
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
	// PageOfOrder body MUST reference Order by component $ref, not
	// inline the Order schema (it has its own component).
	idx := strings.Index(body, "PageOfOrder:")
	if idx < 0 {
		t.Fatal("PageOfOrder not found")
	}
	tail := body[idx : idx+400]
	if !strings.Contains(tail, "$ref: '#/components/schemas/Order'") {
		t.Errorf("PageOfOrder items should $ref Order:\n%s", tail)
	}
}

// TestGenerateOpenAPIGenericInstanceMixinFlatten pins mixin
// preservation through generic substitution: a mixin reference inside
// the generic body must surface in the instance component via the
// `allOf` composition, identical to the non-generic mixin emission.
// An earlier walker dropped mixin members during instantiation -
// `Page<Order>` would lose audit fields its template inherited.
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

// TestGenerateOpenAPIRecursiveGenericTerminatesViaRef pins the
// termination guarantee for self-referential generics like
// `type Tree<T> = { val: T, kids: Tree<T>[] }`. Pre-refactor the
// inlining path would infinite-loop; post-refactor the registry
// short-circuits by returning the already-registered component name
// when the substituted body re-encounters the same instance.
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
	// The kids field is `Tree<T>[]` post-substitution `Tree<Leaf>[]`,
	// which the emitter recognises as the same instance and rewrites
	// to a $ref - the cycle terminator.
	if !strings.Contains(tail, "$ref: '#/components/schemas/TreeOfLeaf'") {
		t.Errorf("TreeOfLeaf body should $ref itself in the kids field:\n%s", tail)
	}
}

// TestGenerateOpenAPIGenericOptionalWraps pins the
// `Page<User>?` → `allOf: [$ref:PageOfUser] + nullable: true` shape.
// Without the wrapper, client codegen (`openapi-typescript`, ...) types
// the field as required-and-non-null even though the server may send
// `null` because the wire decoder accepts both.
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
	if !strings.Contains(tail, "allOf:") {
		t.Errorf("Holder.page should use allOf wrapper for optional ref:\n%s", tail)
	}
	if !strings.Contains(tail, "$ref: '#/components/schemas/PageOfOrder'") {
		t.Errorf("Holder.page should $ref the generic instance:\n%s", tail)
	}
	if !strings.Contains(tail, "nullable: true") {
		t.Errorf("Holder.page should be nullable:\n%s", tail)
	}
}

// TestGenerateOpenAPIGenericResponseTopLevel covers the
// `response Page<Order>` path. The method's response sits directly on
// a generic instance - the per-operation `<Method>RespBody` schema
// must $ref the synthetic component name, not the bare generic decl
// name (which would dangle - the generic decl never gets emitted).
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

// TestGenerateOpenAPIGenericMultiParam ensures multi-param naming
// uses the `And` separator and emits the expected component shape.
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

// TestGenerateOpenAPIServiceLevelSecurityInherit pins the
// service+method security inheritance: a `@security` on the primary
// service applies to every operation, and a method-level `@security`
// adds an OR alternative on top.
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

// TestGenerateOpenAPIIgnoreSecurityClearsInherited pins the
// `@ignoreSecurity` opt-out: a method with this decorator MUST NOT
// inherit the service-level `@security` chain, so the operation
// renders without any `security:` clause (or with an explicit empty
// requirement, which OpenAPI tooling treats as "no auth").
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

// TestGenerateOpenAPIIgnoreTagsClearsInherited mirrors the
// security case for `@ignoreTags`: cleared inherited tags, plus any
// method-level `@tags(...)` start from an empty list.
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

// operationBlock returns the slice of YAML body covering exactly one
// operation. It walks back from the operationId line to the matching
// verb line so sibling fields emitted alphabetically before operationId
// (description, ...) stay inside the block, and trims forward at the
// next path or end-of-paths so the block terminates before the following
// operation's header.
func operationBlock(t *testing.T, body, opID string) string {
	t.Helper()
	idx := strings.Index(body, "\n      operationId: "+opID)
	if idx < 0 {
		t.Fatalf("operation %q not found in:\n%s", opID, body)
	}
	// Walk backward to the nearest verb line above the operationId.
	verbs := []string{"\n    get:\n", "\n    post:\n", "\n    put:\n", "\n    patch:\n", "\n    delete:\n"}
	start := -1
	for _, v := range verbs {
		if s := strings.LastIndex(body[:idx], v); s > start {
			start = s
		}
	}
	if start < 0 {
		start = idx
	}
	// Walk forward from PAST the operationId line to find the next
	// path entry (`  /...:` at two-space indent) or the next verb
	// (which would belong to a sibling operation on the same path).
	// Either marks the end of this operation block.
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

// TestGenerateOpenAPIResponseHeaders pins the response @header /
// @cookie split: fields decorated with @header land in the
// operation's response.headers map, fields decorated with @cookie
// collapse into a Set-Cookie header (OpenAPI 3.x has no first-class
// cookie response slot), and ONLY the unbound fields end up in the
// JSON body schema.
func TestGenerateOpenAPIResponseHeaders(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
type Resp { items string  total string @header("X-Total-Count")  session string @cookie("sid") }
service S { get List /things { response Resp } }`)
	// Header field surfaces under response.headers, NOT body schema.
	if !strings.Contains(body, "X-Total-Count:") {
		t.Errorf("X-Total-Count header missing:\n%s", body)
	}
	// Cookie field collapses into Set-Cookie with a names hint.
	if !strings.Contains(body, "Set-Cookie:") {
		t.Errorf("Set-Cookie header missing:\n%s", body)
	}
	if !strings.Contains(body, "Sets cookies: sid") {
		t.Errorf("cookie names hint missing:\n%s", body)
	}
	// ListRespBody must NOT advertise the header/cookie fields.
	if strings.Contains(body, "total:") && strings.Contains(body, "ListRespBody:\n      properties:\n        items:") {
		// total appears, but make sure it's NOT inside the body schema.
		// A precise check: the body schema's properties block must list
		// only `items`.
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

// TestGenerateOpenAPIErrorMixinFlatten mirrors the type-side mixin
// flatten for ErrorDecl bodies: an `error X { Timestamps; ... }`
// must expose every Timestamps field on the wire, otherwise clients
// pre-parsing error envelopes drop fields they should accept.
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

// TestGenerateOpenAPIOptionalRefNullable checks that an optional
// struct-typed field (`boss User?`) emits nullable in the component
// schema. Bare `$ref` carries no nullable flag; OpenAPI 3.0 expresses
// "ref OR null" via `allOf: [$ref] + nullable: true`. Without the
// wrapper, TS client generators type the field as required `User`
// and refuse `null` even though the server accepts it.
func TestGenerateOpenAPIOptionalRefNullable(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
type Inner { id string }
type T {
    plain    Inner
    optional Inner?
}
service S { post Create /c { request T  response T } }`)
	// Plain Inner stays as bare $ref.
	if !strings.Contains(body, "$ref: '#/components/schemas/Inner'") {
		t.Errorf("plain ref missing:\n%s", body)
	}
	// Optional Inner wraps in allOf + nullable.
	if !strings.Contains(body, "nullable: true") {
		t.Errorf("optional ref should carry nullable:\n%s", body)
	}
	if !strings.Contains(body, "allOf:") {
		t.Errorf("optional ref should use allOf wrapper:\n%s", body)
	}
}

// TestGenerateOpenAPIOptionalEmitsNullable: a `T?` field produces
// `nullable: true` in OpenAPI so spec consumers accept JSON `null`
// for it. The `?` field is also dropped from `required[]`; an
// `@nullable` field stays in required. Both forms agree on
// `nullable: true`.
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
	if err := GenerateOpenAPI(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	out := string(body)

	// Both `a` (T?) and `b` (@nullable) emit nullable: true. The
	// per-operation `CreateReqBody` doubles each, so total = 4.
	if got := strings.Count(out, "nullable: true"); got < 4 {
		t.Errorf("expected nullable: true for T? and @nullable in BOTH T and CreateReqBody (got %d):\n%s", got, out)
	}
	// required[]: c stays (plain), b stays (@nullable), a goes (T?).
	// CreateReqBody mirrors the same shape, so each marker doubles.
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

// TestGenerateOpenAPIPerOperationSchemaMetadata: the inline
// `<Method>Req{Body,Query,Path,Header,Cookie}` schemas carry the
// same field-level decorator effects (@default, @example,
// @nullable, @doc, @deprecated) as the top-level type schema.
// Without applyFieldMetadata in schemaFromFields, per-operation
// schemas would silently drop them.
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
	if err := GenerateOpenAPI(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	out := string(body)
	// Each marker should appear twice: once under the top-level `T`
	// schema, and once under the per-operation `CreateReqBody`.
	// A single occurrence means schemaFromFields skipped
	// applyFieldMetadata.
	for _, want := range []string{
		"example: alice",
		"default: 18",
		"nullable: true",
		"deprecated: true",
		"The display name",
	} {
		got := strings.Count(out, want)
		if got < 2 {
			t.Errorf("expected %q in BOTH T and CreateReqBody (got %d occurrences):\n%s", want, got, out)
		}
	}
}

// TestGenerateOpenAPIDeprecated covers the three @deprecated emission
// sites: type-level marks the schema deprecated, field-level marks
// only that property, and method-level marks the operation. A
// per-decorator string argument lands in the OpenAPI description so
// docs viewers display the deprecation reason inline.
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
	if err := GenerateOpenAPI(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	body := string(out)

	// Type-level: LegacyBook schema is deprecated. The marker shows
	// up directly under the schema name in the YAML, so a small fixed
	// window is enough - and dodges the pitfall of "next schema"
	// indentation (4-space property prefix vs 4-space top-level key).
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

	// Field-level: sku property is deprecated with description.
	if !strings.Contains(body, "use ISBN") {
		t.Errorf("expected field-level deprecation reason:\n%s", body)
	}

	// Method-level: legacy operation deprecated, new operation NOT.
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

// TestGenerateOpenAPIBasePathNotDuplicated regression-tests the bug
// where `basePath: /api` produced path keys like `/api/v1/foo` AND a
// servers[0].url of `/api`, so spec resolvers (kin-openapi, swagger-cli)
// computed the request URL as `/api/api/v1/foo`. After the fix path
// keys are relative and the basePath lives only in servers[0].url.
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
	if err := GenerateOpenAPI(pkg, cfg, root); err != nil {
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

// ---------- @security cross-check ----------

func TestValidateSecurityRefsHappyPath(t *testing.T) {
	pkg := analyze(t, `service S {
    @security(bearerAuth)
    get GetUser /u {}
}`)
	cfg := &config.Config{
		Package: "x/y",
		OpenAPI: config.OpenAPI{
			SecuritySchemes: map[string]config.SecurityScheme{
				"bearerAuth": {Type: "http", Scheme: "bearer"},
			},
		},
	}
	if errs := ValidateSecurityRefs(pkg, cfg); len(errs) != 0 {
		t.Errorf("expected no errors, got: %v", errs)
	}
}

func TestValidateSecurityRefsUnknownScheme(t *testing.T) {
	pkg := analyze(t, `service S {
    @security(BearAuth)
    get GetUser /u {}
}`)
	cfg := &config.Config{
		Package: "x/y",
		OpenAPI: config.OpenAPI{
			SecuritySchemes: map[string]config.SecurityScheme{
				"bearerAuth": {Type: "http", Scheme: "bearer"},
			},
		},
	}
	errs := ValidateSecurityRefs(pkg, cfg)
	if len(errs) == 0 {
		t.Fatal("expected error for unknown scheme")
	}
	joined := strings.Join(errs, "\n")
	if !strings.Contains(joined, `"BearAuth"`) {
		t.Errorf("expected scheme name in error, got: %s", joined)
	}
	if !strings.Contains(joined, "is not declared") {
		t.Errorf("expected declaration error, got: %s", joined)
	}
}

func TestValidateSecurityRefsServiceLevel(t *testing.T) {
	pkg := analyze(t, `@security(typo)
service S {
    get GetUser /u {}
}`)
	cfg := &config.Config{
		Package: "x/y",
		OpenAPI: config.OpenAPI{
			SecuritySchemes: map[string]config.SecurityScheme{
				"bearerAuth": {Type: "http", Scheme: "bearer"},
			},
		},
	}
	errs := ValidateSecurityRefs(pkg, cfg)
	if len(errs) == 0 {
		t.Fatal("expected error for service-level unknown scheme")
	}
}

func TestValidateSecurityRefsPermissiveWhenNoSchemes(t *testing.T) {
	// When the manifest declares no schemes the cross-check is a no-op
	// - projects that haven't migrated continue to work.
	pkg := analyze(t, `service S {
    @security(anything)
    get GetUser /u {}
}`)
	cfg := &config.Config{Package: "x/y"}
	if errs := ValidateSecurityRefs(pkg, cfg); len(errs) != 0 {
		t.Errorf("expected permissive pass-through, got: %v", errs)
	}
}

// TestValidateSecurityRefsIgnoreSecurityNotChecked ensures
// `@ignoreSecurity` is not mistaken for a scheme reference. It is a
// method-level opt-out decorator, not a security requirement, so
// ValidateSecurityRefs should never flag it as "unknown scheme".
func TestValidateSecurityRefsIgnoreSecurityNotChecked(t *testing.T) {
	pkg := analyze(t, `service S {
    @ignoreSecurity
    get GetUser /u {}
}`)
	cfg := &config.Config{
		Package: "x/y",
		OpenAPI: config.OpenAPI{
			SecuritySchemes: map[string]config.SecurityScheme{
				"bearerAuth": {Type: "http", Scheme: "bearer"},
			},
		},
	}
	if errs := ValidateSecurityRefs(pkg, cfg); len(errs) != 0 {
		t.Errorf("@ignoreSecurity must not be treated as a scheme ref: %v", errs)
	}
}

func TestGenerateOpenAPI(t *testing.T) {
	pkg := analyze(t, handlerSampleDSL)
	root := t.TempDir()
	cfg := sampleConfig()
	cfg.OpenAPI.Title = "API"
	cfg.OpenAPI.Version = "1.2.3"
	if err := GenerateOpenAPI(pkg, cfg, root); err != nil {
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
		// /v1/api/v1/users/{id}, matching the runtime listen path.
		"/api/v1/users/{id}",
		"- url: /v1",
		"get:",
		"post:",
		"delete:",
		"operationId: GetUser",
		"#/components/schemas/User",
		// so the schema is defined but not referenced.
		"GetUserReq:",
		"components:",
		"schemas:",
	)
	// Negative: the basePath must NOT appear at the start of any path
	// key - that's the doubled-prefix bug from before the fix.
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
	if err := GenerateOpenAPI(pkg, cfg, root); err != nil {
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
	if err := GenerateOpenAPI(pkg, sampleConfig(), root); err != nil {
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

// TestGenerateOpenAPIPostWithQueryAndPath verifies that a body-bearing
// verb still emits requestBody AND surfaces explicitly-decorated fields
// as parameters. Demonstrates the "POST /resource?dry_run=true" pattern
// with a path id baked in for good measure.
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
	if err := GenerateOpenAPI(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	src := string(out)
	mustContainAll(t, src,
		"requestBody:",
		// Body / Query get grouped schemas; Path stays inline.
		"$ref: '#/components/schemas/CreateReqBody'",
		"CreateReqBody:",
		"CreateReqQuery:",
		// Response side uses the same convention: <Method>RespBody.
		"CreateRespBody:",
		"$ref: '#/components/schemas/CreateRespBody'",
		"in: path",
		"in: query",
		"name: id",
		"name: dryRun",
	)
	// `payload` carries no binding decorator → should NOT appear as a
	// parameter; it stays in the requestBody schema only.
	if strings.Contains(src, "name: payload") {
		t.Errorf("unmarked body field leaked into parameters:\n%s", src)
	}
}

// TestGenerateOpenAPIGetWithBodySkipped confirms that even on a GET, a
// `@body` decorator causes the field to be excluded from parameters.
func TestGenerateOpenAPIGetWithBodySkipped(t *testing.T) {
	pkg := analyze(t, `package design

type ListReq {
    id      string  @path
    cursor  string
    secret  string  @body
}

type Resp { ok bool }

service S {
    get List /things/{id} {
        request   ListReq
        response  Resp
    }
}`)
	root := t.TempDir()
	if err := GenerateOpenAPI(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	src := string(out)
	if !strings.Contains(src, "name: cursor") {
		t.Errorf("expected default-query field in parameters:\n%s", src)
	}
	if strings.Contains(src, "name: secret") {
		t.Errorf("@body field should not appear in parameters:\n%s", src)
	}
}

// TestGenerateOpenAPICookieAndHeaderInline pins the rule that Cookie /
// Header / Path bins stay inline as parameters (no <Method>Req<Kind>
// schemas), while Body and Query DO get their own grouped schemas.
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
	if err := GenerateOpenAPI(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	src := string(out)
	mustContainAll(t, src,
		"CallReqBody:",
		"CallReqQuery:",
		"CallReqHeader:",
		"CallReqCookie:",
		"CallReqPath:",
		"in: header",
		"name: apiKey",
		"in: cookie",
		"name: session",
	)
	// Parameter schemas are emitted inline (by value) rather than
	// `$ref`-ing into the wrapper `<Method>Req<Kind>` schema. Nested
	// `$ref` is technically valid JSON-Pointer but breaks most TS /
	// Java / Rust client generators (hey-api, openapi-typescript,
	// openapi-generator < 7) which fail to derive a stable type name
	// from the property-walk path.
	mustContainNone(t, src,
		"$ref: '#/components/schemas/CallReqHeader/properties/",
		"$ref: '#/components/schemas/CallReqCookie/properties/",
		"$ref: '#/components/schemas/CallReqQuery/properties/",
		"$ref: '#/components/schemas/CallReqPath/properties/",
	)
}

// TestGenerateOpenAPITagsFromDecorators covers @tags resolution at
// service level + method level + the empty fallback. Confirms both the
// string-literal and bare-identifier argument forms are accepted.
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
	if err := GenerateOpenAPI(pkg, sampleConfig(), root); err != nil {
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

// TestGenerateOpenAPIOperationIDDefaultAndOverride pins the rule:
// default operationId = method name verbatim (PascalCase from DSL),
// override = whatever string literal `@operationId("...")` supplies.
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
	if err := GenerateOpenAPI(pkg, sampleConfig(), root); err != nil {
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

// TestGenerateOpenAPITagsWithSpaces confirms tag values are written
// verbatim - including spaces. YAML output quotes them automatically
// when the value contains a space, so consumer tooling reads back the
// exact original string.
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
	if err := GenerateOpenAPI(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	src := string(out)
	mustContainAll(t, src,
		// YAML quotes the space-containing string when emitting.
		`- user management`,
		`- v1`,
	)
}

func TestGenerateOpenAPIMissingPackage(t *testing.T) {
	pkg := &semantic.Package{}
	if err := GenerateOpenAPI(pkg, sampleConfig(), t.TempDir()); err == nil {
		t.Fatal("expected error")
	}
}

// TestGenerateOpenAPIPerModeMediaTypes pins the content-type a method
// emits for each handler mode. Without this guard, the dispatch in
// buildOperation could regress to plain `application/json` and the
// spec would silently disagree with the runtime wire format.
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
	if err := GenerateOpenAPI(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(out)
	// Passthrough endpoint advertises `*/*` for its response body
	// because the framework lets logic write whatever wire format it
	// likes - there is no schema to publish.
	mustContainAll(t, src,
		"'*/*'",
		"multipart/form-data",
		"format: binary",
	)
	// Multipart endpoint must NOT advertise application/json for the
	// request body - file uploads only flow through multipart.
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
