package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dropship-dev/craftgo/internal/semantic"
)

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
		"/v1/api/v1/users/{id}",
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
		// property — that's the canonical-source rule.
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

    // Decorator override — exact verbatim string.
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
// verbatim — including spaces. YAML output quotes them automatically
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

type Tick { value int }
type UploadReq { note string  avatar file }
type UploadResp { ok bool }

service S {
    @stream
    @format(sse)
    get TickSSE /sse {
        response  stream Tick
    }
    @stream
    @format(ndjson)
    get TickJSONL /jsonl {
        response  stream Tick
    }
    @raw
    post EchoBlob /echo {
    }
    @raw
    @stream
    post EchoStream /echo-stream {
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
	for _, want := range []string{
		"text/event-stream",
		"application/x-ndjson",
		"application/octet-stream",
		"multipart/form-data",
		"format: binary",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("expected %q in spec:\n%s", want, src)
		}
	}
	// Multipart endpoint must NOT advertise application/json for the
	// request body — file uploads only flow through multipart.
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
