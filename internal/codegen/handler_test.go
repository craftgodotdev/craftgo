package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/config"
	craftparser "github.com/dropship-dev/craftgo/internal/parser"
	"github.com/dropship-dev/craftgo/internal/semantic"
)

const handlerSampleDSL = `package design

type GetUserReq { id string }
type UpdateUserReq { id string  name string }
type User { id string  name string }

@prefix("/api/v1")
service UserService {
    get GetUser /users/{id} {
        request   GetUserReq
        response  User
    }
    post UpdateUser /users/{id} {
        request   UpdateUserReq
        response  User
    }
    delete DeleteUser /users/{id} {
        response  User
    }
}

extend service UserService {
    @doc("simple ping")
    get Ping {
    }
}`

func sampleConfig() *config.Config {
	return &config.Config{
		Package: "github.com/example/app",
		Output: config.Output{
			Types:      "./internal/types",
			Handler:    "./internal/handler",
			Routes:     "./internal/routes",
			Logic:      "./internal/logic",
			Svccontext: "./svccontext/svccontext.go",
			OpenAPI:    "./docs/openapi.yaml",
		},
		OpenAPI: config.OpenAPI{BasePath: "/v1"},
	}
}

func analyzePkg(t *testing.T, src string) *semantic.Package {
	t.Helper()
	p := craftparser.New("test.craftgo", src)
	f := p.Parse()
	if d := p.Diagnostics(); len(d) > 0 {
		t.Fatalf("parse errors: %v", d)
	}
	pkg, diags := semantic.Analyze([]*ast.File{f})
	if len(diags) > 0 {
		t.Fatalf("semantic errors: %v", diags)
	}
	return pkg
}

// ---------- streamCtor mapping ----------

func TestStreamCtorMapping(t *testing.T) {
	cases := map[string]string{
		"":               "SSE",
		"sse":            "SSE",
		"ndjson":         "NDJSON",
		"jsonl":          "NDJSON",
		"jsonarray":      "JSONArray",
		"csv":            "CSV",
		"concat":         "Concat",
		"lengthprefixed": "LengthPrefixed",
		"unknown":        "SSE",
	}
	for in, want := range cases {
		if got := streamCtor(in); got != want {
			t.Errorf("streamCtor(%q) = %q, want %q", in, got, want)
		}
	}
}

// ---------- handler ----------

func TestGenerateHandlersAllVerbs(t *testing.T) {
	pkg := analyzePkg(t, handlerSampleDSL)
	root := t.TempDir()
	cfg := sampleConfig()
	if err := GenerateHandlers(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "internal/handler/user-service")
	files := []string{"get-user-handler.go", "update-user-handler.go", "delete-user-handler.go", "ping-handler.go"}
	for _, fn := range files {
		out, err := os.ReadFile(filepath.Join(dir, fn))
		if err != nil {
			t.Fatalf("missing %s: %v", fn, err)
		}
		mustParseGo(t, string(out))
	}

	// GET handler should have request decode skipped (no body verb).
	getSrc, _ := os.ReadFile(filepath.Join(dir, "get-user-handler.go"))
	if strings.Contains(string(getSrc), "json.NewDecoder(r.Body)") {
		t.Errorf("GET handler must not decode body:\n%s", getSrc)
	}
	// POST handler must decode body.
	postSrc, _ := os.ReadFile(filepath.Join(dir, "update-user-handler.go"))
	if !strings.Contains(string(postSrc), "json.NewDecoder(r.Body)") {
		t.Errorf("POST handler must decode body:\n%s", postSrc)
	}
	// Ping (no request, no response decl) → empty body call + 204.
	pingSrc, _ := os.ReadFile(filepath.Join(dir, "ping-handler.go"))
	if !strings.Contains(string(pingSrc), "l.Ping()") {
		t.Errorf("Ping handler should call l.Ping() with no arg:\n%s", pingSrc)
	}
	if !strings.Contains(string(pingSrc), "http.StatusNoContent") {
		t.Errorf("Ping handler should write 204:\n%s", pingSrc)
	}
}

// TestGenerateHandlersDefaults pins the @default pre-fill emission. The
// handler must assign each declared default BEFORE the JSON decode so
// fields absent from the body keep the DSL value; explicit fields in
// the body still overwrite via the standard decoder semantics.
func TestGenerateHandlersDefaults(t *testing.T) {
	src := `package design
type Req {
    name   string  @default("anon")
    limit  int     @default(20)
    ratio  float64 @default(0.5)
    active bool    @default(true)
    plain  string
}
service S {
    post Make /make {
        request   Req
    }
}`
	pkg := analyzePkg(t, src)
	root := t.TempDir()
	if err := GenerateHandlers(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(root, "internal/handler/s/make-handler.go"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(out)
	mustParseGo(t, body)
	for _, want := range []string{
		`req.Name = "anon"`,
		"req.Limit = 20",
		"req.Ratio = 0.5",
		"req.Active = true",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in handler:\n%s", want, body)
		}
	}
	// The non-defaulted `plain` field must NOT receive an assignment.
	if strings.Contains(body, "req.Plain =") {
		t.Errorf("plain field shouldn't be pre-filled:\n%s", body)
	}
	// Pre-fill must precede JSON decode so absent body fields keep
	// their default.
	if dec := strings.Index(body, "json.NewDecoder"); dec >= 0 {
		if pre := body[:dec]; !strings.Contains(pre, `req.Name = "anon"`) {
			t.Error("expected default assignments before json.NewDecoder")
		}
	}
}

func TestGenerateHandlersResponseHeaderCookie(t *testing.T) {
	src := `package design
type DownloadReq { id string }
type DownloadResp {
    body       string
    etag       string @header
    sessionID  string @cookie
}
service FilesService {
    get Download /files/{id} {
        request   DownloadReq
        response  DownloadResp
    }
}`
	pkg := analyzePkg(t, src)
	root := t.TempDir()
	if err := GenerateHandlers(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(root, "internal/handler/files-service/download-handler.go"))
	if err != nil {
		t.Fatal(err)
	}
	mustParseGo(t, string(out))
	body := string(out)
	checks := []string{
		`w.Header().Set("etag", resp.Etag)`,
		`http.SetCookie(w, &http.Cookie{Name: "sessionID", Value: resp.SessionID})`,
		`w.Header().Set("Content-Type", "application/json")`,
	}
	for _, want := range checks {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in handler:\n%s", want, body)
		}
	}
	// Ensure header/cookie writes precede the body encoder so they hit the
	// wire before WriteHeader implicitly fires.
	if idx := strings.Index(body, "json.NewEncoder"); idx >= 0 {
		pre := body[:idx]
		if !strings.Contains(pre, "w.Header().Set(\"etag\"") {
			t.Error("expected response header write to precede json.NewEncoder")
		}
	}

	// And the response struct should hide etag/sessionID from the JSON body.
	typesOut, err := os.ReadFile(filepath.Join(root, "internal/types", "design", "types.go"))
	if err == nil {
		// types.go is generated separately; only assert when present.
		typesSrc := string(typesOut)
		if strings.Contains(typesSrc, `Etag string `+"`json:\"etag\"`") {
			t.Errorf("etag field should be tagged json:\"-\":\n%s", typesSrc)
		}
	}
}

func TestGenerateTypesNonBodyBindingsAreSkipped(t *testing.T) {
	pkg := analyzePkg(t, `package design
type Req {
    id      string @path
    q       string @query
    auth    string @header
    sess    string @cookie
    payload string
}`)
	dir := t.TempDir()
	if err := GenerateTypes(pkg, dir); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "types.go"))
	src := string(out)
	mustParseGo(t, src)
	// Each non-body-bound field must have json:"-" on the same line.
	for _, ident := range []string{"ID", "Q", "Auth", "Sess"} {
		if !lineHas(src, ident, `json:"-"`) {
			t.Errorf("expected %q with json:\"-\" tag:\n%s", ident, src)
		}
	}
	if !lineHas(src, "Payload", `json:"payload"`) {
		t.Errorf("expected payload field to keep its JSON tag:\n%s", src)
	}
}

// lineHas reports whether `src` has a line containing both `ident` and
// `tag`. Used by the binding tests because gofmt may align field columns
// with extra whitespace, defeating literal substring matches.
func lineHas(src, ident, tag string) bool {
	for _, line := range strings.Split(src, "\n") {
		if strings.Contains(line, ident) && strings.Contains(line, tag) {
			return true
		}
	}
	return false
}

func TestGenerateHandlersMissingPackageName(t *testing.T) {
	pkg := &semantic.Package{Services: map[string]*semantic.ServiceInfo{}}
	if err := GenerateHandlers(pkg, sampleConfig(), t.TempDir()); err == nil {
		t.Fatal("expected error for empty package name")
	}
}

func TestGenerateHandlerHelpers(t *testing.T) {
	pkg := analyzePkg(t, handlerSampleDSL)
	root := t.TempDir()
	cfg := sampleConfig()
	if err := GenerateHandlerHelpers(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(root, "internal/handler/user-service/errors.go"))
	if err != nil {
		t.Fatal(err)
	}
	mustParseGo(t, string(out))
	if !strings.Contains(string(out), "func writeError") {
		t.Errorf("expected writeError helper:\n%s", out)
	}
}

func TestGenerateHandlerHelpersMissingPackageName(t *testing.T) {
	pkg := &semantic.Package{Services: map[string]*semantic.ServiceInfo{}}
	if err := GenerateHandlerHelpers(pkg, sampleConfig(), t.TempDir()); err == nil {
		t.Fatal("expected error for empty package name")
	}
}

// ---------- routes ----------

func TestGenerateRoutesPatterns(t *testing.T) {
	pkg := analyzePkg(t, handlerSampleDSL)
	root := t.TempDir()
	cfg := sampleConfig()
	if err := GenerateRoutes(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(root, "internal/routes/user-service/routes.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(out)
	mustParseGo(t, src)
	for _, want := range []string{
		`"GET /v1/api/v1/users/{id}"`,
		`"POST /v1/api/v1/users/{id}"`,
		`"DELETE /v1/api/v1/users/{id}"`,
		`handler.GetUserHandler(svcCtx)`,
		`handler.PingHandler(svcCtx)`,
		`func RegisterRoutes(srv *server.Server, svcCtx`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("expected %q in:\n%s", want, src)
		}
	}
}

func TestGenerateRoutesMissingPackageName(t *testing.T) {
	pkg := &semantic.Package{Services: map[string]*semantic.ServiceInfo{}}
	if err := GenerateRoutes(pkg, sampleConfig(), t.TempDir()); err == nil {
		t.Fatal("expected error for empty package name")
	}
}

// TestGenerateRoutesMultipleMiddlewares pins the chaining order: when a
// method declares `@middlewares(A, B, C)` AND its service declares
// `@middlewares(S)`, the generated `srv.With(...)` call lists service-
// level middlewares first (outermost) followed by the method's, in
// source order. Server.With wraps in reverse so S is the outermost
// frame and C is closest to the handler.
func TestGenerateRoutesMultipleMiddlewares(t *testing.T) {
	pkg := analyzePkg(t, `package design

type Thing { id string }

middleware Auth
middleware RateLimit
middleware RequestCounter

@prefix("/v1")
@middlewares(Auth)
service S {
    @middlewares(RateLimit, RequestCounter)
    get GetThing /things/{id} {
        response Thing
    }
}`)
	root := t.TempDir()
	cfg := sampleConfig()
	if err := GenerateRoutes(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(root, "internal/routes/s/routes.go"))
	src := string(out)
	mustParseGo(t, src)
	want := `svcCtx.Auth(svcCtx.RateLimit(svcCtx.RequestCounter(handler.GetThingHandler(svcCtx))))`
	if !strings.Contains(src, want) {
		t.Errorf("expected ordered middleware chain %q in:\n%s", want, src)
	}
}

// TestGenerateRoutesGroupAddsPathSegment confirms `@group("...")` on a
// service stitches its argument into the route between the @prefix and
// the method path. A service with `@prefix("/v1") @group("admin")`
// produces `/<basePath>/v1/admin/<method-path>`.
func TestGenerateRoutesGroupAddsPathSegment(t *testing.T) {
	pkg := analyzePkg(t, `package design

type Thing { id string }

@prefix("/v1")
@group("admin")
service AdminService {
    get ListAll /things {
        response Thing
    }
    get Health {
    }
}`)
	root := t.TempDir()
	cfg := sampleConfig()
	cfg.OpenAPI.BasePath = "/api"
	if err := GenerateRoutes(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(root, "internal/routes/admin-service/routes.go"))
	src := string(out)
	mustParseGo(t, src)
	for _, want := range []string{
		`"GET /api/v1/admin/things"`,
		`"GET /api/v1/admin/health"`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("expected %q in:\n%s", want, src)
		}
	}
}

// TestGenerateRoutesMethodLimits pins the runtime-limit wrapper for
// methods declaring `@readTimeout` / `@writeTimeout` / `@maxBodySize`.
// Routes get wrapped in `server.WithLimits(handler, server.Limits{...})`
// at the call site; the routes file imports "time" because the
// emitted literal uses time.Millisecond / time.Second helpers.
func TestGenerateRoutesMethodLimits(t *testing.T) {
	src := `package design
type Req { x string }
service S {
    @readTimeout(500ms)
    @maxBodySize(1024)
    post Make /m { request Req }
}`
	pkg := analyzePkg(t, src)
	root := t.TempDir()
	if err := GenerateRoutes(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(root, "internal/routes/s/routes.go"))
	body := string(out)
	mustParseGo(t, body)
	for _, want := range []string{
		`"time"`,
		"server.WithLimits",
		"ReadTimeout: 500 * time.Millisecond",
		"MaxBodySize: 1024",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in routes:\n%s", want, body)
		}
	}
}

// TestGenerateRoutesStreamSkipsReadTimeout pins the streaming-method
// carve-out: `@readTimeout` is silently dropped when the method also
// has `@stream` because http.TimeoutHandler would cut the stream
// mid-flight.
func TestGenerateRoutesStreamSkipsReadTimeout(t *testing.T) {
	src := `package design
type Tick { i int }
service S {
    @stream
    @readTimeout(5s)
    @maxBodySize(2048)
    get Live /live { response stream Tick }
}`
	pkg := analyzePkg(t, src)
	root := t.TempDir()
	if err := GenerateRoutes(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(root, "internal/routes/s/routes.go"))
	src2 := string(body)
	if strings.Contains(src2, "ReadTimeout:") {
		t.Errorf("streaming method should not get ReadTimeout:\n%s", src2)
	}
	if !strings.Contains(src2, "MaxBodySize:") {
		t.Errorf("MaxBodySize should still apply to streams:\n%s", src2)
	}
}

func TestGenerateRoutesNoBasePathNoPrefix(t *testing.T) {
	pkg := analyzePkg(t, `package design

type Req {}
type Resp {}

service Bare {
    get GetThing /thing {
        request Req
        response Resp
    }
    get Root {
        response Resp
    }
}`)
	root := t.TempDir()
	cfg := sampleConfig()
	cfg.OpenAPI.BasePath = ""
	if err := GenerateRoutes(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(root, "internal/routes/bare/routes.go"))
	src := string(out)
	mustParseGo(t, src)
	if !strings.Contains(src, `"GET /thing"`) {
		t.Errorf("expected GET /thing pattern:\n%s", src)
	}
	if !strings.Contains(src, `"GET /root"`) {
		t.Errorf("expected fallback GET /root pattern when no path declared:\n%s", src)
	}
}

// ---------- logic ----------

func TestGenerateLogicScaffold(t *testing.T) {
	pkg := analyzePkg(t, handlerSampleDSL)
	root := t.TempDir()
	cfg := sampleConfig()
	if err := GenerateLogic(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "internal/logic/user-service")
	for _, fn := range []string{"get-user-logic.go", "update-user-logic.go", "ping-logic.go"} {
		out, err := os.ReadFile(filepath.Join(dir, fn))
		if err != nil {
			t.Fatalf("missing %s: %v", fn, err)
		}
		mustParseGo(t, string(out))
	}
	// Ping has no request/response → method takes no args, returns error only.
	pingSrc, _ := os.ReadFile(filepath.Join(dir, "ping-logic.go"))
	if !strings.Contains(string(pingSrc), "func (l *PingLogic) Ping() error {") {
		t.Errorf("Ping logic signature mismatch:\n%s", pingSrc)
	}
	getSrc, _ := os.ReadFile(filepath.Join(dir, "get-user-logic.go"))
	if !strings.Contains(string(getSrc), "func (l *GetUserLogic) GetUser(req *types.GetUserReq) (*types.User, error)") {
		t.Errorf("GetUser logic signature mismatch:\n%s", getSrc)
	}
}

func TestGenerateLogicSkipsExisting(t *testing.T) {
	pkg := analyzePkg(t, handlerSampleDSL)
	root := t.TempDir()
	cfg := sampleConfig()
	dir := filepath.Join(root, cfg.Output.Logic, "userservice")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(dir, "get-user-logic.go")
	custom := []byte("package userservice\n// user-owned\n")
	if err := os.WriteFile(existing, custom, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := GenerateLogic(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(existing)
	if string(out) != string(custom) {
		t.Errorf("scaffold overwrote user file:\n%s", out)
	}
}

func TestGenerateLogicMissingPackageName(t *testing.T) {
	pkg := &semantic.Package{Services: map[string]*semantic.ServiceInfo{}}
	if err := GenerateLogic(pkg, sampleConfig(), t.TempDir()); err == nil {
		t.Fatal("expected error for empty package name")
	}
}

// ---------- paths ----------

func TestPathHelpers(t *testing.T) {
	if got := goImportFromRel("github.com/x/y", "./internal/types"); got != "github.com/x/y/internal/types" {
		t.Errorf("got %q", got)
	}
	if got := goImportFromRel("github.com/x/y", ""); got != "github.com/x/y" {
		t.Errorf("empty rel: got %q", got)
	}
	if got := goImportFromRel("github.com/x/y", "internal/x/"); got != "github.com/x/y/internal/x" {
		t.Errorf("trailing slash: got %q", got)
	}
	if got := goImportFromRel("github.com/x/y", `internal\handler`); got != "github.com/x/y/internal/handler" {
		t.Errorf("backslash: got %q", got)
	}
	if got := fileDirRel("./svccontext/svccontext.go"); got != "svccontext" {
		t.Errorf("got %q", got)
	}
	if got := fileDirRel("main.go"); got != "" {
		t.Errorf("got %q (expected empty)", got)
	}
	if !hasBodyVerb("post") || !hasBodyVerb("PUT") || !hasBodyVerb("PATCH") {
		t.Error("expected body verbs to be true")
	}
	if hasBodyVerb("GET") || hasBodyVerb("DELETE") {
		t.Error("expected non-body verbs to be false")
	}
	if servicePrefix(nil) != "" {
		t.Error("nil service should yield empty prefix")
	}
	// service with non-prefix decorator should yield "".
	svc := &ast.ServiceDecl{
		Decorators: []*ast.Decorator{
			{Name: "tags", Args: []*ast.DecoratorArg{{Value: &ast.StringLit{Value: "x"}}}},
			{Name: "prefix"}, // no args → ignored
			{Name: "prefix", Args: []*ast.DecoratorArg{{Value: &ast.IntLit{Value: 1}}}}, // wrong type → ignored
		},
	}
	if got := servicePrefix(svc); got != "" {
		t.Errorf("got %q", got)
	}
}

// ---------- end-to-end ----------

func TestGeneratePipelineEndToEnd(t *testing.T) {
	pkg := analyzePkg(t, handlerSampleDSL)
	root := t.TempDir()
	cfg := sampleConfig()
	for _, step := range []func() error{
		func() error { return GenerateHandlers(pkg, cfg, root) },
		func() error { return GenerateHandlerHelpers(pkg, cfg, root) },
		func() error { return GenerateRoutes(pkg, cfg, root) },
		func() error { return GenerateLogic(pkg, cfg, root) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
}

// ---------- mode dispatch: stream / raw / raw-stream / multipart ----------

const streamSampleDSL = `package design

type Tick { value int }

service FeedService {
    @stream
    @format(sse)
    get TickStream /ticks {
        response  stream Tick
    }
    @stream
    @format(ndjson)
    get TickJSON /ticks/jsonl {
        response  stream Tick
    }
}`

func TestGenerateHandlersStreamSSEAndNDJSON(t *testing.T) {
	pkg := analyzePkg(t, streamSampleDSL)
	root := t.TempDir()
	cfg := sampleConfig()
	if err := GenerateHandlers(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	if err := GenerateLogic(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	hDir := filepath.Join(root, "internal/handler/feed-service")
	lDir := filepath.Join(root, "internal/logic/feed-service")

	sseHandler, _ := os.ReadFile(filepath.Join(hDir, "tick-stream-handler.go"))
	mustParseGo(t, string(sseHandler))
	for _, want := range []string{"server.NewSSEStream(w", "l.TickStream(stream)"} {
		if !strings.Contains(string(sseHandler), want) {
			t.Errorf("SSE handler missing %q:\n%s", want, sseHandler)
		}
	}
	if strings.Contains(string(sseHandler), "json.NewDecoder") {
		t.Errorf("SSE handler must not decode JSON body:\n%s", sseHandler)
	}

	ndHandler, _ := os.ReadFile(filepath.Join(hDir, "tick-json-handler.go"))
	mustParseGo(t, string(ndHandler))
	if !strings.Contains(string(ndHandler), "server.NewNDJSONStream(w") {
		t.Errorf("NDJSON handler should call NewNDJSONStream:\n%s", ndHandler)
	}

	sseLogic, _ := os.ReadFile(filepath.Join(lDir, "tick-stream-logic.go"))
	mustParseGo(t, string(sseLogic))
	if !strings.Contains(string(sseLogic), "func (l *TickStreamLogic) TickStream(stream *server.SSEStream) error") {
		t.Errorf("SSE logic signature mismatch:\n%s", sseLogic)
	}
}

const rawSampleDSL = `package design

service BlobService {
    @raw
    post EchoBlob /echo {
    }
}`

func TestGenerateHandlersRawBypassesJSON(t *testing.T) {
	pkg := analyzePkg(t, rawSampleDSL)
	root := t.TempDir()
	cfg := sampleConfig()
	if err := GenerateHandlers(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	if err := GenerateLogic(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	handler, _ := os.ReadFile(filepath.Join(root, "internal/handler/blob-service/echo-blob-handler.go"))
	mustParseGo(t, string(handler))
	for _, want := range []string{"io.Copy(w, out)", "l.EchoBlob(body)", "io.Reader"} {
		if !strings.Contains(string(handler), want) {
			t.Errorf("raw handler missing %q:\n%s", want, handler)
		}
	}
	if strings.Contains(string(handler), "json.NewDecoder") || strings.Contains(string(handler), "json.NewEncoder") {
		t.Errorf("raw handler must not touch JSON:\n%s", handler)
	}

	logic, _ := os.ReadFile(filepath.Join(root, "internal/logic/blob-service/echo-blob-logic.go"))
	mustParseGo(t, string(logic))
	if !strings.Contains(string(logic), "func (l *EchoBlobLogic) EchoBlob(body io.Reader) (io.Reader, error)") {
		t.Errorf("raw logic signature mismatch:\n%s", logic)
	}
}

const rawStreamSampleDSL = `package design

service PipeService {
    @raw
    @stream
    post EchoStream /echo-stream {
    }
}`

func TestGenerateHandlersRawStreamCombo(t *testing.T) {
	pkg := analyzePkg(t, rawStreamSampleDSL)
	root := t.TempDir()
	cfg := sampleConfig()
	if err := GenerateHandlers(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	if err := GenerateLogic(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	handler, _ := os.ReadFile(filepath.Join(root, "internal/handler/pipe-service/echo-stream-handler.go"))
	mustParseGo(t, string(handler))
	for _, want := range []string{"server.NewRawStream(w", "l.EchoStream(r.Body, stream)"} {
		if !strings.Contains(string(handler), want) {
			t.Errorf("raw-stream handler missing %q:\n%s", want, handler)
		}
	}
	if strings.Contains(string(handler), "json.NewDecoder") || strings.Contains(string(handler), "json.NewEncoder") {
		t.Errorf("raw-stream handler must not touch JSON:\n%s", handler)
	}

	logic, _ := os.ReadFile(filepath.Join(root, "internal/logic/pipe-service/echo-stream-logic.go"))
	mustParseGo(t, string(logic))
	if !strings.Contains(string(logic), "func (l *EchoStreamLogic) EchoStream(body io.Reader, stream *server.RawStream) error") {
		t.Errorf("raw-stream logic signature mismatch:\n%s", logic)
	}
}

const multipartSampleDSL = `package design

type UploadReq {
    note      string
    avatar    file
}
type UploadResp { ok bool }

service UploadService {
    post Upload /upload {
        request   UploadReq
        response  UploadResp
    }
}`

func TestGenerateHandlersMultipartFromFileField(t *testing.T) {
	pkg := analyzePkg(t, multipartSampleDSL)
	root := t.TempDir()
	cfg := sampleConfig()
	if err := GenerateHandlers(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	handler, _ := os.ReadFile(filepath.Join(root, "internal/handler/upload-service/upload-handler.go"))
	mustParseGo(t, string(handler))
	for _, want := range []string{
		"r.ParseMultipartForm(",
		`r.FormValue("note")`,
		`r.FormFile("avatar")`,
		"req.Avatar = header",
	} {
		if !strings.Contains(string(handler), want) {
			t.Errorf("multipart handler missing %q:\n%s", want, handler)
		}
	}
	if strings.Contains(string(handler), "json.NewDecoder(r.Body)") {
		t.Errorf("multipart handler must not JSON-decode body:\n%s", handler)
	}
}

// TestGenerateHandlersRejectsBadQueryShapes pins the codegen-time
// rejection of unsupported query-binding shapes. Before this gate
// existed, struct/[]struct/map fields on a GET request were silently
// dropped — the handler omitted the bind line and the field landed
// at the logic layer zero-valued, with no error to chase.
//
// Each case deliberately constructs a request type that exercises one
// rejection branch:
//   - Filter Point      → struct on @query
//   - Tags  []Point     → []struct on @query
//   - Meta  map<string,string> → map on @query
//   - Page  Page<Book>  → generic on @query
//   - id    int @path    → non-string on @path
//   - auth  int @header  → non-string on @header
//   - sid   int @cookie  → non-string on @cookie
//   - opt   int? @query  → optional numeric on @query (no clean v1 idiom)
func TestGenerateHandlersRejectsBadQueryShapes(t *testing.T) {
	cases := []struct {
		label   string
		dsl     string
		want    string // substring expected in the error message
	}{
		{
			label: "struct on @query",
			dsl: `package design
type Point { x int  y int }
type SearchReq { filter Point @query }
service S { get Search /search { request SearchReq } }`,
			want: "filter",
		},
		{
			label: "[]struct on @query",
			dsl: `package design
type Point { x int  y int }
type SearchReq { tags Point[] @query }
service S { get Search /search { request SearchReq } }`,
			want: "tags",
		},
		{
			label: "map on @query",
			dsl: `package design
type SearchReq { meta map<string,string> @query }
service S { get Search /search { request SearchReq } }`,
			want: "meta",
		},
		{
			label: "generic on @query",
			dsl: `package design
type Book { id string }
type Page<T> { items T[] }
type SearchReq { page Page<Book> @query }
service S { get Search /search { request SearchReq } }`,
			want: "page",
		},
		{
			label: "non-string on @path",
			dsl: `package design
type GetReq { id int @path }
service S { get Get /items/{id} { request GetReq } }`,
			want: "@path requires",
		},
		{
			label: "non-string on @header",
			dsl: `package design
type GetReq { auth int @header }
service S { get Get /items { request GetReq } }`,
			want: "@header requires",
		},
		{
			label: "non-string on @cookie",
			dsl: `package design
type GetReq { sid int @cookie }
service S { get Get /items { request GetReq } }`,
			want: "@cookie requires",
		},
		{
			label: "optional numeric on @query",
			dsl: `package design
type SearchReq { opt int? @query }
service S { get Search /search { request SearchReq } }`,
			want: "optional",
		},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			pkg := analyzePkg(t, tc.dsl)
			err := GenerateHandlers(pkg, sampleConfig(), t.TempDir())
			if err == nil {
				t.Fatalf("expected rejection, got nil error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q missing %q", err.Error(), tc.want)
			}
		})
	}
}
