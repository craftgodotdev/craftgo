package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// ---------- raw modes: @rawRequest / @rawResponse / @passthrough ----------

// TestBuildSignatureEveryCell pins the stub declaration and the transport
// call for every request × response × raw-side cell. The two templates
// read the same methodSignature, so this table is the single place the
// public gen-once contract is spelled out.
func TestBuildSignatureEveryCell(t *testing.T) {
	cases := []struct {
		name     string
		mode     methodMode
		params   string
		results  string
		callArgs string
		result   bool
		http     bool
	}{
		{"typed both", methodMode{HasRequest: true, HasResponse: true}, "req *types.Req", "(*types.Resp, error)", "&req", true, false},
		{"typed req only", methodMode{HasRequest: true}, "req *types.Req", "error", "&req", false, false},
		{"typed resp only", methodMode{HasResponse: true}, "", "(*types.Resp, error)", "", true, false},
		{"typed neither", methodMode{}, "", "error", "", false, false},
		{"rawResponse + req + resp", methodMode{HasRequest: true, HasResponse: true, RawResponse: true}, "w http.ResponseWriter, r *http.Request, req *types.Req", "error", "w, r, &req", false, true},
		{"rawResponse + req", methodMode{HasRequest: true, RawResponse: true}, "w http.ResponseWriter, r *http.Request, req *types.Req", "error", "w, r, &req", false, true},
		{"rawResponse + resp", methodMode{HasResponse: true, RawResponse: true}, "w http.ResponseWriter, r *http.Request", "error", "w, r", false, true},
		{"rawResponse bare", methodMode{RawResponse: true}, "w http.ResponseWriter, r *http.Request", "error", "w, r", false, true},
		{"rawRequest + req + resp", methodMode{HasRequest: true, HasResponse: true, RawRequest: true}, "r *http.Request", "(*types.Resp, error)", "r", true, true},
		{"rawRequest + resp", methodMode{HasResponse: true, RawRequest: true}, "r *http.Request", "(*types.Resp, error)", "r", true, true},
		{"rawRequest + req", methodMode{HasRequest: true, RawRequest: true}, "r *http.Request", "error", "r", false, true},
		{"rawRequest bare", methodMode{RawRequest: true}, "r *http.Request", "error", "r", false, true},
		{"passthrough + blocks", methodMode{HasRequest: true, HasResponse: true, RawRequest: true, RawResponse: true}, "w http.ResponseWriter, r *http.Request", "error", "w, r", false, true},
		{"passthrough bare", methodMode{RawRequest: true, RawResponse: true}, "w http.ResponseWriter, r *http.Request", "error", "w, r", false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildSignature(c.mode, "types.Req", "types.Resp")
			want := methodSignature{Params: c.params, Results: c.results, CallArgs: c.callArgs, HasResult: c.result, NeedsHTTP: c.http}
			if got != want {
				t.Errorf("buildSignature(%+v) =\n  %+v\nwant\n  %+v", c.mode, got, want)
			}
		})
	}
}

const rawModesSampleDSL = `package design

type ListReq {
    page  int?    @query @default(1)
    tag   string? @query
}
type UserList { ids string[]  total int @header("X-Total") }
type IngestResult { accepted int }
type Event { kind string  payload string }
type UploadReq { note string  avatar file }
type Item { id string @path }

service DemoService {
    @rawResponse
    get ListUsers /users {
        request  ListReq
        response UserList
    }
    @rawResponse
    @status(201)
    get Snapshot /snapshot {
        response UserList
    }
    @rawResponse
    post Upload /upload {
        request UploadReq
    }
    @rawRequest
    post Ingest /ingest {
        request  IngestResult
        response IngestResult
    }
    @rawRequest
    post Drain /drain {}
    @passthrough
    get Events /events/{id} {
        request  Item
        response Event
    }
    @passthrough
    get Metrics /metrics {}
}`

// renderGoldenBundle generates the transport handlers and service stubs
// for pkg into a scratch root and returns every emitted Go file joined
// into one document, each prefixed by its project-relative path, so a
// single golden pins the whole surface of one scenario.
func renderGoldenBundle(t *testing.T, pkg *semantic.Package, r *ProjectResolver) string {
	t.Helper()
	root := t.TempDir()
	cfg := sampleConfig()
	if err := GenerateTransportResolved(pkg, cfg, root, r); err != nil {
		t.Fatalf("transport: %v", err)
	}
	var cross CrossPkg
	if r != nil {
		cross = r.CrossPkg
	}
	if err := GenerateServicePackage(pkg, cfg, root, cross); err != nil {
		t.Fatalf("service: %v", err)
	}
	var paths []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".go") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	var sb strings.Builder
	for _, p := range paths {
		rel, _ := filepath.Rel(root, p)
		body, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&sb, "// ==== %s ====\n", filepath.ToSlash(rel))
		sb.Write(body)
		sb.WriteString("\n")
	}
	return sb.String()
}

// genRawModes generates the raw-modes sample into a temp root and returns
// a reader over the transport and service files by method file name.
func genRawModes(t *testing.T) func(kind, file string) string {
	t.Helper()
	pkg := analyze(t, rawModesSampleDSL)
	root := t.TempDir()
	cfg := sampleConfig()
	if err := GenerateTransport(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	if err := GenerateService(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	return func(kind, file string) string {
		b, err := os.ReadFile(filepath.Join(root, "internal", kind, "demo-service", file))
		if err != nil {
			t.Fatal(err)
		}
		mustParseGo(t, string(b))
		return string(b)
	}
}

func TestGenerateRawResponseBindsThenHandsWriter(t *testing.T) {
	read := genRawModes(t)
	h := read("transport", "list-users.go")
	mustContainAll(t, h,
		"var req types.ListReq",
		"req.Page = &__d",
		`_q.Get("page")`,
		"if err := req.Validate(); err != nil {",
		"if err := l.ListUsers(w, r, &req); err != nil {",
		"server.WriteError(w, r, err)",
	)
	// The response side is logic's: no encode, no status, no response
	// header plumbing, no strconv for the documented X-Total header.
	mustContainNone(t, h,
		"server.JSON().Encode",
		"w.Header().Set(",
		"w.WriteHeader(",
		`"strconv"`,
	)
	s := read("service", "list-users.go")
	mustContainAll(t, s,
		`"net/http"`,
		"func (l *ListUsersService) ListUsers(w http.ResponseWriter, r *http.Request, req *types.ListReq) error {",
		"documented as\n// types.UserList",
		`http.Error(w, "not implemented", http.StatusNotImplemented)`,
	)
}

func TestGenerateRawResponseNoRequestSkipsBind(t *testing.T) {
	read := genRawModes(t)
	h := read("transport", "snapshot.go")
	mustContainAll(t, h, "if err := l.Snapshot(w, r); err != nil {")
	// @status(201) is docs-only on a raw response: the handler must not
	// write it, and nothing references the types package.
	mustContainNone(t, h, "var req", "w.WriteHeader(", "http.StatusCreated", `types "`)
	s := read("service", "snapshot.go")
	mustContainAll(t, s, "func (l *SnapshotService) Snapshot(w http.ResponseWriter, r *http.Request) error {")
	mustContainNone(t, s, `types "`)
}

func TestGenerateRawResponseOverMultipart(t *testing.T) {
	read := genRawModes(t)
	h := read("transport", "upload.go")
	mustContainAll(t, h,
		"r.ParseMultipartForm(",
		"defer func() { _ = r.MultipartForm.RemoveAll() }()",
		`r.FormValue("note")`,
		`r.FormFile("avatar")`,
		"if err := req.Validate(); err != nil {",
		"if err := l.Upload(w, r, &req); err != nil {",
	)
	mustContainNone(t, h, "server.JSON().Decode", "server.JSON().Encode", "w.WriteHeader(")
}

func TestGenerateRawRequestHandsRequestThenEncodes(t *testing.T) {
	read := genRawModes(t)
	h := read("transport", "ingest.go")
	mustContainAll(t, h,
		"resp, err := l.Ingest(r)",
		`w.Header().Set("Content-Type", "application/json; charset=utf-8")`,
		"w.WriteHeader(http.StatusCreated)",
		"_ = server.JSON().Encode(w, resp)",
	)
	// The docs-only request block must not be bound, decoded or imported.
	mustContainNone(t, h, "var req", "server.JSON().Decode", "req.Validate()", `types "`)
	s := read("service", "ingest.go")
	mustContainAll(t, s,
		"func (l *IngestService) Ingest(r *http.Request) (*types.IngestResult, error) {",
		"documented as\n// types.IngestResult",
		"return nil, nil",
	)
}

func TestGenerateRawRequestNoResponseWritesStatus(t *testing.T) {
	read := genRawModes(t)
	h := read("transport", "drain.go")
	mustContainAll(t, h, "err := l.Drain(r)", "w.WriteHeader(http.StatusNoContent)")
	mustContainNone(t, h, `types "`, "server.JSON().Encode")
	s := read("service", "drain.go")
	mustContainAll(t, s, "func (l *DrainService) Drain(r *http.Request) error {")
	mustContainNone(t, s, `types "`)
}

func TestGeneratePassthroughWithBlocksKeepsSignature(t *testing.T) {
	read := genRawModes(t)
	h := read("transport", "events.go")
	mustContainAll(t, h, "if err := l.Events(w, r); err != nil {")
	mustContainNone(t, h, "var req", `types "`, "server.JSON()")
	s := read("service", "events.go")
	mustContainAll(t, s,
		"func (l *EventsService) Events(w http.ResponseWriter, r *http.Request) error {",
		"decode the request into types.Item yourself",
		"write a body matching types.Event",
	)
	mustContainNone(t, s, `types "`)
	// A bare passthrough still carries no contract paragraph.
	bare := read("service", "metrics.go")
	if strings.Contains(bare, "docs-only contract") {
		t.Errorf("bare @passthrough stub must not mention a contract:\n%s", bare)
	}
}

// TestParityPassthroughEqualsBothFlags pins that `@passthrough` and
// `@rawRequest @rawResponse` are one mode: transport, stub and OpenAPI
// output are byte-identical (the analyser only adds a redundancy warning
// for the two-flag spelling).
func TestParityPassthroughEqualsBothFlags(t *testing.T) {
	src := func(decorators string) string {
		return `package design
type Item { id string @path }
type Event { kind string }
service S {
    ` + decorators + `
    get Events /events/{id} {
        request  Item
        response Event
    }
}`
	}
	render := func(dsl string) (string, string) {
		pkg := analyze(t, dsl)
		return renderGoldenBundle(t, pkg, nil), generateOpenAPIToString(t, dsl)
	}
	goA, apiA := render(src("@passthrough"))
	goB, apiB := render(src("@rawRequest\n    @rawResponse"))
	if goA != goB {
		t.Errorf("transport/service parity broken:\n%s", firstDiff(goA, goB))
	}
	if apiA != apiB {
		t.Errorf("openapi parity broken:\n%s", firstDiff(apiA, apiB))
	}
}

// TestParityTransportCallMatchesStubSignature pins that the transport
// call and the stub declaration come from the same methodSignature for
// every cell - the decide-once guard against the two drifting apart.
func TestParityTransportCallMatchesStubSignature(t *testing.T) {
	pkg := analyze(t, rawModesSampleDSL)
	svc := pkg.Services["DemoService"]
	if svc == nil {
		t.Fatal("DemoService missing")
	}
	cfg := sampleConfig()
	for _, m := range svc.Methods {
		imps := importPathsForGroup(cfg, pkg, "DemoService", "")
		td, err := buildTransportData("DemoService", m, imps, pkg, nil)
		if err != nil {
			t.Fatalf("%s: %v", m.Name, err)
		}
		sd := buildServiceData(pkg.Name, "DemoService", m, imps, nil)
		if td.Sig != sd.Sig {
			t.Errorf("%s: transport signature %+v != service signature %+v", m.Name, td.Sig, sd.Sig)
		}
	}
}

// ---------- OpenAPI: a block on a raw side is the documented contract ----------

func TestGenerateOpenAPIRawSidesDocumentBlocks(t *testing.T) {
	src := `package design
type Item { id string @path  q string? @query }
type Event { kind string }
type Created { id string  loc string @header("Location") }
type Body { name string }
type FileReq { note string  doc file }

service S {
    @passthrough
    get PtBlocks /pt/{id} { request Item  response Event }
    @passthrough
    get PtBare /pt/bare/{id} {}
    @rawResponse
    @status(201)
    post RrStatus /rr/status { request Body  response Created }
    @rawResponse
    post RrPost /rr/post { request Body  response Event }
    @rawResponse
    get RrBare /rr/bare {}
    @rawRequest
    post RqPost /rq/post { request Body  response Event }
    @rawRequest
    post RqFile /rq/file { request FileReq  response Event }
    @rawRequest
    get RqBare /rq/bare/{id} {}
}`
	spec := generateOpenAPIToString(t, src)

	// @passthrough with blocks: typed parameters and $ref bodies, no */*.
	pt := operationBlock(t, spec, "PtBlocks")
	mustContainAll(t, pt, "name: id", "in: path", "name: q", "in: query", "$ref: '#/components/schemas/PtBlocksRespBody'", `"200"`)
	mustContainNone(t, pt, "'*/*'")

	// No block on a raw side: bare string path params and */* as before.
	bare := operationBlock(t, spec, "PtBare")
	mustContainAll(t, bare, "name: id", "in: path", "type: string", "'*/*'")
	mustContainNone(t, bare, "$ref")

	// Raw response: @status(201) is the documented code and the response
	// @header field is documented too (both are logic's to write).
	rr := operationBlock(t, spec, "RrStatus")
	mustContainAll(t, rr, `"201"`, "$ref: '#/components/schemas/RrStatusRespBody'", "Location:")
	mustContainNone(t, rr, `"200"`)

	// Raw response on POST without @status: 200, not the verb-aware 201 -
	// logic writes whatever status it wants.
	rrPost := operationBlock(t, spec, "RrPost")
	mustContainAll(t, rrPost, `"200"`, "$ref: '#/components/schemas/RrPostRespBody'")
	mustContainNone(t, rrPost, `"201"`)

	// Raw response with no block keeps */*.
	rrBare := operationBlock(t, spec, "RrBare")
	mustContainAll(t, rrBare, "'*/*'")

	// Raw request on POST with a typed response: the framework writes the
	// response, so the verb-aware 201 default still applies, and the
	// docs-only request block is a JSON requestBody.
	rq := operationBlock(t, spec, "RqPost")
	mustContainAll(t, rq, `"201"`, "requestBody:", "application/json", "$ref: '#/components/schemas/RqPostReqBody'")

	// Raw request whose contract has a file field documents multipart even
	// though the transport never parses it.
	rqFile := operationBlock(t, spec, "RqFile")
	mustContainAll(t, rqFile, "multipart/form-data", "format: binary")

	// Raw request with no block on a parameterised path: bare string param.
	rqBare := operationBlock(t, spec, "RqBare")
	mustContainAll(t, rqBare, "name: id", "in: path", "type: string")
}

// ---------- raw modes × every other method-level decorator ----------

const rawModesMixDSL = `package design

middleware Auth
middleware Audit

error NotFound ThingMissing { id string }
error Conflict ThingTaken { id string }

type Req { id string @path @length(1, 32)  q string? @query }
type Resp { ok bool  trace string @header("X-Trace") }
type Body { name string @minLength(1) }

@prefix("/mix")
@tags(mix)
@security(Bearer)
@middlewares(Auth)
service MixService {
    @doc("raw response with everything")
    @summary("RR all")
    @operationId("rrAll")
    @tags(extra)
    @security(Admin)
    @errors(ThingMissing, ThingTaken)
    @status(202)
    @timeout(3s)
    @maxBodySize(2MB)
    @middlewares(Audit)
    @deprecated("use RrPlain")
    @rawResponse
    post RrAll /rr/{id} { request Req  response Resp }

    @ignoreMiddleware
    @ignoreSecurity
    @ignoreTags
    @rawResponse
    get RrIgnore /rr/ignore {}

    @status(202)
    @errors(ThingMissing)
    @rawRequest
    post RqAll /rq { request Body  response Resp }

    @doc("same doc")
    @passthrough
    @rawRequest
    @rawResponse
    get Triple /triple/{id} { request Req  response Resp }

    @doc("same doc")
    @passthrough
    get PtRef /ptref/{id} { request Req  response Resp }
}`

// TestRawModesMixWithMethodDecorators pins that the raw flags compose with
// every other method-level decorator: limits and middlewares still land in
// routes.go, the docs-only side still carries @status / @errors / @security /
// @deprecated / @tags / @summary / @operationId into OpenAPI, the @ignore*
// family still clears the inherited chains, and all three flags on one
// method generate exactly what @passthrough generates.
func TestRawModesMixWithMethodDecorators(t *testing.T) {
	pkg := analyze(t, rawModesMixDSL)
	root := t.TempDir()
	cfg := sampleConfig()
	if err := GenerateTransport(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	if err := GenerateService(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	if err := GenerateRoutes(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		mustParseGo(t, string(b))
		return string(b)
	}

	routes := read("internal/routes/mix-service/routes.go")
	mustContainAll(t, routes,
		"server.WithLimits(transport.RrAll(svcCtx), server.Limits{Timeout: 3 * time.Second, MaxBodySize: 2097152}), svcCtx.Auth, svcCtx.Audit)",
		"transport.RrIgnore(svcCtx))",
	)
	mustContainNone(t, routes, "RrIgnore(svcCtx), svcCtx")

	handler := read("internal/transport/mix-service/rr-all.go")
	mustContainAll(t, handler, `req.ID = r.PathValue("id")`, "if err := req.Validate(); err != nil {", "if err := l.RrAll(w, r, &req); err != nil {")
	mustContainNone(t, handler, "w.WriteHeader(", "http.StatusAccepted")

	// All three flags == @passthrough, byte for byte (same doc so the
	// rendered comments match too).
	rename := func(s string) string {
		return strings.ReplaceAll(strings.ReplaceAll(s, "Triple", "PtRef"), "triple", "ptref")
	}
	for _, kind := range []string{"transport", "service"} {
		got := rename(read("internal/" + kind + "/mix-service/triple.go"))
		want := read("internal/" + kind + "/mix-service/pt-ref.go")
		if got != want {
			t.Errorf("%s: three flags must render exactly like @passthrough:\n%s", kind, firstDiff(want, got))
		}
	}

	spec := generateOpenAPIToString(t, rawModesMixDSL)
	rr := operationBlock(t, spec, "rrAll")
	mustContainAll(t, rr,
		"deprecated: true", "Deprecated: use RrPlain", "summary: RR all",
		`"202"`, "$ref: '#/components/schemas/RrAllRespBody'", "X-Trace:",
		`"404"`, `"409"`, "- Admin: []", "- extra",
	)
	mustContainNone(t, rr, `"201"`, "'*/*'")
	rq := operationBlock(t, spec, "RqAll")
	mustContainAll(t, rq, `"202"`, "$ref: '#/components/schemas/RqAllReqBody'", "$ref: '#/components/schemas/RqAllRespBody'", `"404"`)
	ign := operationBlock(t, spec, "RrIgnore")
	mustContainAll(t, ign, "'*/*'")
	mustContainNone(t, ign, "Bearer", "- mix")
	// The last path in the document drags the trailing `servers:` key
	// into its block; trim it so the two operations compare cleanly.
	opBlock := func(id string) string {
		b := operationBlock(t, spec, id)
		if i := strings.Index(b, "\nservers:"); i >= 0 {
			b = b[:i]
		}
		return b
	}
	if rename(opBlock("Triple")) != opBlock("PtRef") {
		t.Errorf("three flags must document exactly like @passthrough:\n%s", firstDiff(opBlock("PtRef"), rename(opBlock("Triple"))))
	}
}
