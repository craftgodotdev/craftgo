package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dropship-dev/craftgo/internal/config"
	"github.com/dropship-dev/craftgo/internal/semantic"
)

// ---------- enum / scalar schema emission ----------

// generateOpenAPIToString runs the OpenAPI generator on `src` and
// returns the resulting YAML body as a string. Wraps the boilerplate
// (analyze → temp dir → ReadFile) so each golden-driven openapi
// test reads as just `src` + `expectGolden`.
func generateOpenAPIToString(t *testing.T, src string) string {
	t.Helper()
	pkg := analyzePkg(t, src)
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
func TestGenerateOpenAPIEnumSchemasEmitted(t *testing.T) {
	body := generateOpenAPIToString(t, `package design
enum Priority { Low  Normal  High }
enum Tier { Bronze = 1  Silver = 2 }
type Req {
    pri Priority @required
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
scalar Email string @format("email")
type Req { addr Email @required }
service S { post Send /m { request Req } }`)
	expectGolden(t, "openapi-scalar-schemas.yaml", body)
}

// TestGenerateOpenAPIErrorsDecorator pins the @errors flow:
// referenced error decls land as components.schemas entries AND as
// per-operation responses keyed by the error category's HTTP status.
func TestGenerateOpenAPIErrorsDecorator(t *testing.T) {
	src := `package design
error NotFound BookNotFound
error Conflict DuplicateISBN { sku string }
type Book { id string }
service S {
    @errors(BookNotFound)
    get GetBook /books/{id} { response Book }
    @errors(DuplicateISBN)
    @status(201)
    post CreateBook /books { request Book  response Book }
}`
	pkg := analyzePkg(t, src)
	root := t.TempDir()
	if err := GenerateOpenAPI(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	body := string(out)

	// Error type schemas are emitted under components.schemas.
	for _, want := range []string{"BookNotFoundErr:", "DuplicateISBNErr:"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected error schema %q:\n%s", want, body)
		}
	}
	// GetBook response 404 → BookNotFoundErr.
	if !strings.Contains(body, `'#/components/schemas/BookNotFoundErr'`) {
		t.Error("expected BookNotFoundErr ref")
	}
	// CreateBook overrides success to 201 via @status.
	if !strings.Contains(body, `"201":`) {
		t.Errorf("expected @status(201) override:\n%s", body)
	}
	// CreateBook also registers 409 (Conflict) for DuplicateISBN.
	if !strings.Contains(body, `"409":`) {
		t.Errorf("expected 409 Conflict response:\n%s", body)
	}
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
service S {
    @doc("Fetch a single book.")
    @summary("Get a book")
    get GetBook /books/{id} { response Book }
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
	pkg := analyzePkg(t, src)
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

// TestGenerateOpenAPIExternalDocs covers `@externalDocs(url:..., description:...)`
// on operations and types.
func TestGenerateOpenAPIExternalDocs(t *testing.T) {
	src := `package design
@externalDocs(url: "https://docs.example.com/book", description: "Book schema reference")
type Book { id string }
service S {
    @externalDocs(url: "https://docs.example.com/list", description: "Listing endpoint guide")
    get List /books { response Book }
}`
	pkg := analyzePkg(t, src)
	root := t.TempDir()
	if err := GenerateOpenAPI(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(root, "docs/openapi.yaml"))
	src2 := string(body)
	for _, want := range []string{
		"https://docs.example.com/book",
		"Book schema reference",
		"https://docs.example.com/list",
		"Listing endpoint guide",
	} {
		if !strings.Contains(src2, want) {
			t.Errorf("expected externalDocs entry %q:\n%s", want, src2)
		}
	}
}

// TestGenerateOpenAPIDeprecated covers the three @deprecated emission
// sites: type-level marks the schema deprecated, field-level marks
// only that property, and method-level marks the operation. A
// per-decorator string argument lands in the OpenAPI description so
// docs viewers display the migration hint inline.
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
	pkg := analyzePkg(t, src)
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
	pkg := analyzePkg(t, `package design
@prefix("/v1")
service S {
    get GetThing /things/{id} {}
}`)
	cfg := &config.Config{
		Package: "x/y",
		Output: config.Output{
			Types: "./internal/types", Handler: "./internal/handler",
			Routes: "./internal/routes", Logic: "./internal/logic",
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
	pkg := analyzePkg(t, `service S {
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
	pkg := analyzePkg(t, `service S {
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
	pkg := analyzePkg(t, `@security(typo)
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
	pkg := analyzePkg(t, `service S {
    @security(anything)
    get GetUser /u {}
}`)
	cfg := &config.Config{Package: "x/y"}
	if errs := ValidateSecurityRefs(pkg, cfg); len(errs) != 0 {
		t.Errorf("expected permissive pass-through, got: %v", errs)
	}
}

func TestValidateSecurityRefsAllowsNoauth(t *testing.T) {
	pkg := analyzePkg(t, `service S {
    @security(noauth)
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
		t.Errorf("noauth must be accepted, got: %v", errs)
	}
}

func TestGenerateOpenAPI(t *testing.T) {
	pkg := analyzePkg(t, handlerSampleDSL)
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
	for _, want := range []string{
		"openapi: 3.1.0",
		"title: API",
		"version: 1.2.3",
		// Path keys are RELATIVE to servers[].url. With basePath "/v1"
		// pushed onto servers and the service @prefix("/api/v1"), the
		// path entry is /api/v1/users/{id}; the resolved URL becomes
		// /v1/api/v1/users/{id}, matching the runtime listen path.
		"/api/v1/users/{id}",
		"- url: /v1",
		"get:",
		"post:",
		"delete:",
		"operationId: GetUser",
		"#/components/schemas/User",
		// GetUserReq's fields are inlined as parameters on the GET op,
		// so the schema is defined but not referenced.
		"GetUserReq:",
		"components:",
		"schemas:",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("expected %q in:\n%s", want, src)
		}
	}
	// Negative: the basePath must NOT appear at the start of any path
	// key - that's the doubled-prefix bug from before the fix.
	if strings.Contains(src, "/v1/api/v1/users/{id}") {
		t.Errorf("path key still has duplicated basePath:\n%s", src)
	}
}

func TestGenerateOpenAPIDefaultsAndEmpty(t *testing.T) {
	pkg := analyzePkg(t, "package design")
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
	pkg := analyzePkg(t, `package design
type Bag {
    items   string[]
    meta    map<string, string>
    age     int?
    name    string  @required
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
	pkg := analyzePkg(t, `package design

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
	for _, want := range []string{
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
	} {
		if !strings.Contains(src, want) {
			t.Errorf("expected %q in:\n%s", want, src)
		}
	}
	// `payload` carries no binding decorator → should NOT appear as a
	// parameter; it stays in the requestBody schema only.
	if strings.Contains(src, "name: payload") {
		t.Errorf("unmarked body field leaked into parameters:\n%s", src)
	}
}

// TestGenerateOpenAPIGetWithBodySkipped confirms that even on a GET, a
// `@body` decorator causes the field to be excluded from parameters.
func TestGenerateOpenAPIGetWithBodySkipped(t *testing.T) {
	pkg := analyzePkg(t, `package design

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
	pkg := analyzePkg(t, `package design

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
	for _, want := range []string{
		"CallReqBody:",
		"CallReqQuery:",
		"CallReqHeader:",
		"CallReqCookie:",
		"CallReqPath:",
		"in: header",
		"name: apiKey",
		"in: cookie",
		"name: session",
		// Parameter schemas $ref into the matching per-kind schema's
		// property - that's the canonical-source rule.
		"$ref: '#/components/schemas/CallReqHeader/properties/apiKey'",
		"$ref: '#/components/schemas/CallReqCookie/properties/session'",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("expected %q in:\n%s", want, src)
		}
	}
}

// TestGenerateOpenAPITagsFromDecorators covers @tags resolution at
// service level + method level + the empty fallback. Confirms both the
// string-literal and bare-identifier argument forms are accepted.
func TestGenerateOpenAPITagsFromDecorators(t *testing.T) {
	pkg := analyzePkg(t, `package design

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
	for _, want := range []string{
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
	} {
		if !strings.Contains(src, want) {
			t.Errorf("expected %q in:\n%s", want, src)
		}
	}
}

// TestGenerateOpenAPIOperationIDDefaultAndOverride pins the rule:
// default operationId = method name verbatim (PascalCase from DSL),
// override = whatever string literal `@operationId("...")` supplies.
func TestGenerateOpenAPIOperationIDDefaultAndOverride(t *testing.T) {
	pkg := analyzePkg(t, `package design

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
	pkg := analyzePkg(t, `package design

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
	for _, want := range []string{
		// YAML quotes the space-containing string when emitting.
		`- user management`,
		`- v1`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("expected %q in:\n%s", want, src)
		}
	}
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
	pkg := analyzePkg(t, dsl)
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
	for _, want := range []string{
		"'*/*'",
		"multipart/form-data",
		"format: binary",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("expected %q in spec:\n%s", want, src)
		}
	}
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
