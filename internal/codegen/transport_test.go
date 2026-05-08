package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	craftparser "github.com/craftgodotdev/craftgo/internal/parser"
	"github.com/craftgodotdev/craftgo/internal/semantic"
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
			Transport:  "./internal/transport",
			Routes:     "./internal/routes",
			Service:    "./internal/service",
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

// ---------- handler ----------

func TestGenerateTransportAllVerbs(t *testing.T) {
	pkg := analyzePkg(t, handlerSampleDSL)
	root := t.TempDir()
	cfg := sampleConfig()
	if err := GenerateTransport(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "internal/transport/user-service")
	files := []string{"get-user.go", "update-user.go", "delete-user.go", "ping.go"}
	for _, fn := range files {
		out, err := os.ReadFile(filepath.Join(dir, fn))
		if err != nil {
			t.Fatalf("missing %s: %v", fn, err)
		}
		mustParseGo(t, string(out))
	}

	// GET handler should have request decode skipped (no body verb).
	getSrc, _ := os.ReadFile(filepath.Join(dir, "get-user.go"))
	if strings.Contains(string(getSrc), "json.NewDecoder(r.Body)") {
		t.Errorf("GET handler must not decode body:\n%s", getSrc)
	}
	// POST handler must decode body.
	postSrc, _ := os.ReadFile(filepath.Join(dir, "update-user.go"))
	if !strings.Contains(string(postSrc), "json.NewDecoder(r.Body)") {
		t.Errorf("POST handler must decode body:\n%s", postSrc)
	}
	// Ping (no request, no response decl) → empty body call + 204.
	pingSrc, _ := os.ReadFile(filepath.Join(dir, "ping.go"))
	if !strings.Contains(string(pingSrc), "l.Ping()") {
		t.Errorf("Ping handler should call l.Ping() with no arg:\n%s", pingSrc)
	}
	if !strings.Contains(string(pingSrc), "http.StatusNoContent") {
		t.Errorf("Ping handler should write 204:\n%s", pingSrc)
	}
}

// TestGenerateTransportDefaults pins the? @default pre-fill emission. The
// handler must assign each declared default BEFORE the JSON decode so
// fields absent from the body keep the DSL value; explicit fields in
// the body still overwrite via the standard decoder semantics.
func TestGenerateTransportDefaults(t *testing.T) {
	src := `package design
type Req {
    name string?  @default("anon")
    limit int?     @default(20)
    ratio float64? @default(0.5)
    active bool?    @default(true)
    plain  string
}
service S {
    post Make /make {
        request   Req
    }
}`
	pkg := analyzePkg(t, src)
	root := t.TempDir()
	if err := GenerateTransport(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(root, "internal/transport/s/make.go"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(out)
	mustParseGo(t, body)
	// Optional `?` fields with @default emit pointer-wrapped pre-fill:
	// `__d := value; req.Field = &__d` so the JSON decoder leaves a
	// nil pointer alone but reports the default through the typed
	// pointer when the field is absent.
	for _, want := range []string{
		`__d := "anon"`,
		`req.Name = &__d`,
		`__d := 20`,
		`req.Limit = &__d`,
		`__d := 0.5`,
		`req.Ratio = &__d`,
		`__d := true`,
		`req.Active = &__d`,
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
		if pre := body[:dec]; !strings.Contains(pre, `__d := "anon"`) {
			t.Error("expected default assignments before json.NewDecoder")
		}
	}
}

// TestGenerateTransportEnumScalarBindings pins the path / query /
// header / cookie binding for fields whose declared type is an
// enum or a scalar - the generated handler must cast the wire
// string into the typed alias so the request struct lands as
// `Status` / `Email` / etc., letting `req.Validate()` pick up the
// scalar's inherited validators (`@format(email)` ...) and the
// enum's value-set check.
func TestGenerateTransportEnumScalarBindings(t *testing.T) {
	src := `package design

enum Status { Active  Inactive  Pending }
enum Priority { Low = 1  High = 2 }

scalar Email string @format(email) @maxLength(254)
scalar Cents int @min(0) @max(1000000)

type ListReq {
    state    Status   @path
    priority Priority @query
    contact  Email    @query
    cap      Cents    @query
    sess     string   @cookie
    role     Status   @header
}

service S {
    get List /items/{state} { request ListReq }
}`
	pkg := analyzePkg(t, src)
	root := t.TempDir()
	if err := GenerateTransport(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, "internal/transport/s/list.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	mustParseGo(t, got)
	for _, want := range []string{
		// path: string-backed enum cast
		`req.State = types.Status(r.PathValue("state"))`,
		// query string-backed enum + scalar
		`req.Contact = types.Email(r.URL.Query().Get("contact"))`,
		// query int-backed enum: parse + cast through int
		`types.Priority(int(_n))`,
		// query numeric scalar: parse + cast
		`types.Cents(int(_n))`,
		// cookie cast
		`req.Sess = c.Value`,
		// header cast (string-backed enum)
		`req.Role = types.Status(r.Header.Get("role"))`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in handler:\n%s", want, got)
		}
	}
}

// TestGenerateTransportDefaultEnum pins the enum-aware @default
// emission: `@default(Active)` on a `Status`-typed field renders as
// `req.Field = StatusActive` (the Go const buildEnumView produces),
// not as the bare DSL identifier "Active" which wouldn't compile.
func TestGenerateTransportDefaultEnum(t *testing.T) {
	src := `package design
enum Status { Active  Inactive  Pending }
type Req {
    st Status? @default(Pending)
    plain  string
}
service S {
    post Make /make { request Req }
}`
	pkg := analyzePkg(t, src)
	root := t.TempDir()
	if err := GenerateTransport(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(root, "internal/transport/s/make.go"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(out)
	mustParseGo(t, body)
	for _, want := range []string{
		"__d := types.StatusPending",
		"req.St = &__d",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected enum default pre-fill %q:\n%s", want, body)
		}
	}
}

func TestGenerateTransportResponseHeaderCookie(t *testing.T) {
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
	if err := GenerateTransport(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(root, "internal/transport/files-service/download.go"))
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

func TestGenerateTransportMissingPackageName(t *testing.T) {
	pkg := &semantic.Package{Services: map[string]*semantic.ServiceInfo{}}
	if err := GenerateTransport(pkg, sampleConfig(), t.TempDir()); err == nil {
		t.Fatal("expected error for empty package name")
	}
}

func TestGenerateTransportHelpers(t *testing.T) {
	pkg := analyzePkg(t, handlerSampleDSL)
	root := t.TempDir()
	cfg := sampleConfig()
	if err := GenerateTransportHelpers(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(root, "internal/transport/user-service/errors.go"))
	if err != nil {
		t.Fatal(err)
	}
	mustParseGo(t, string(out))
	if !strings.Contains(string(out), "func writeError") {
		t.Errorf("expected writeError helper:\n%s", out)
	}
	// writeError must dispatch on the WriteResponseHeaders interface so
	// typed errors with @header / @cookie fields land their wire writes
	// before the JSON body is encoded.
	for _, want := range []string{
		"WriteResponseHeaders(http.ResponseWriter)",
		"hw.WriteResponseHeaders(w)",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("writeError missing %q:\n%s", want, out)
		}
	}
}

func TestGenerateTransportHelpersMissingPackageName(t *testing.T) {
	pkg := &semantic.Package{Services: map[string]*semantic.ServiceInfo{}}
	if err := GenerateTransportHelpers(pkg, sampleConfig(), t.TempDir()); err == nil {
		t.Fatal("expected error for empty package name")
	}
}

// ---------- routes ----------

// TestGenerateRoutesPatterns pins the canonical routes-emit shape
// (verb + path pattern, handler call, RegisterRoutes signature). The
// snapshot beats listing 6 substring checks: a regression shows the
// entire diverging hunk inline so the user immediately sees what
// changed instead of grepping for one missing string.
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
	mustParseGo(t, string(out))
	expectGolden(t, "routes-user-service.go", string(out))
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
	want := `svcCtx.Auth(svcCtx.RateLimit(svcCtx.RequestCounter(transport.GetThing(svcCtx))))`
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
// methods declaring `@timeout` / `@maxBodySize`. Routes get wrapped
// in `server.WithLimits(handler, server.Limits{...})` at the call
// site; the routes file imports "time" because the emitted literal
// uses time.Millisecond / time.Second helpers.
func TestGenerateRoutesMethodLimits(t *testing.T) {
	src := `package design
type Req { x string }
service S {
    @timeout(500ms)
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
		"Timeout: 500 * time.Millisecond",
		"MaxBodySize: 1024",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in routes:\n%s", want, body)
		}
	}
}

// TestGenerateRoutesPassthroughSkipsTimeout pins the passthrough
// carve-out: `@timeout` is silently dropped when the method also
// carries `@passthrough` because the framework hands the writer to
// logic verbatim and `http.TimeoutHandler` would cut whatever stream
// logic decides to produce. `@maxBodySize` still applies (the body
// cap fires at read time, not response time).
func TestGenerateRoutesPassthroughSkipsTimeout(t *testing.T) {
	src := `package design
service S {
    @passthrough
    @timeout(5s)
    @maxBodySize(2048)
    get Live /live {}
}`
	pkg := analyzePkg(t, src)
	root := t.TempDir()
	if err := GenerateRoutes(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(root, "internal/routes/s/routes.go"))
	src2 := string(body)
	if strings.Contains(src2, "Timeout:") {
		t.Errorf("passthrough method should not get Timeout:\n%s", src2)
	}
	if !strings.Contains(src2, "MaxBodySize:") {
		t.Errorf("MaxBodySize should still apply to passthrough:\n%s", src2)
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

func TestGenerateServiceScaffold(t *testing.T) {
	pkg := analyzePkg(t, handlerSampleDSL)
	root := t.TempDir()
	cfg := sampleConfig()
	if err := GenerateService(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "internal/service/user-service")
	for _, fn := range []string{"get-user.go", "update-user.go", "ping.go"} {
		out, err := os.ReadFile(filepath.Join(dir, fn))
		if err != nil {
			t.Fatalf("missing %s: %v", fn, err)
		}
		mustParseGo(t, string(out))
	}
	// Ping has no request/response → method takes no args, returns error only.
	pingSrc, _ := os.ReadFile(filepath.Join(dir, "ping.go"))
	if !strings.Contains(string(pingSrc), "func (l *PingService) Ping() error {") {
		t.Errorf("Ping logic signature mismatch:\n%s", pingSrc)
	}
	getSrc, _ := os.ReadFile(filepath.Join(dir, "get-user.go"))
	if !strings.Contains(string(getSrc), "func (l *GetUserService) GetUser(req *types.GetUserReq) (*types.User, error)") {
		t.Errorf("GetUser logic signature mismatch:\n%s", getSrc)
	}
}

func TestGenerateServiceSkipsExisting(t *testing.T) {
	pkg := analyzePkg(t, handlerSampleDSL)
	root := t.TempDir()
	cfg := sampleConfig()
	dir := filepath.Join(root, cfg.Output.Service, "userservice")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(dir, "get-user.go")
	custom := []byte("package userservice\n// user-owned\n")
	if err := os.WriteFile(existing, custom, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := GenerateService(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(existing)
	if string(out) != string(custom) {
		t.Errorf("scaffold overwrote user file:\n%s", out)
	}
}

func TestGenerateServiceMissingPackageName(t *testing.T) {
	pkg := &semantic.Package{Services: map[string]*semantic.ServiceInfo{}}
	if err := GenerateService(pkg, sampleConfig(), t.TempDir()); err == nil {
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
		func() error { return GenerateTransport(pkg, cfg, root) },
		func() error { return GenerateTransportHelpers(pkg, cfg, root) },
		func() error { return GenerateRoutes(pkg, cfg, root) },
		func() error { return GenerateService(pkg, cfg, root) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
}

// ---------- mode dispatch: passthrough / multipart ----------

const passthroughSampleDSL = `package design

service FeedService {
    @passthrough
    get LiveTail /tail {
    }
    @passthrough
    get UserTail /users/{id}/tail {
    }
}`

func TestGenerateHandlerPassthrough(t *testing.T) {
	pkg := analyzePkg(t, passthroughSampleDSL)
	root := t.TempDir()
	cfg := sampleConfig()
	if err := GenerateTransport(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	if err := GenerateTransportHelpers(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	if err := GenerateService(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	hDir := filepath.Join(root, "internal/transport/feed-service")
	lDir := filepath.Join(root, "internal/service/feed-service")

	handler, _ := os.ReadFile(filepath.Join(hDir, "live-tail.go"))
	hSrc := string(handler)
	mustParseGo(t, hSrc)
	for _, want := range []string{
		"l.LiveTail(w, r)",
		"writeError(w, err)",
		"http.HandlerFunc",
	} {
		if !strings.Contains(hSrc, want) {
			t.Errorf("passthrough handler missing %q:\n%s", want, hSrc)
		}
	}
	for _, banned := range []string{"json.NewDecoder", "json.NewEncoder", "req.Validate", "types."} {
		if strings.Contains(hSrc, banned) {
			t.Errorf("passthrough handler should not contain %q:\n%s", banned, hSrc)
		}
	}

	logic, _ := os.ReadFile(filepath.Join(lDir, "live-tail.go"))
	lSrc := string(logic)
	mustParseGo(t, lSrc)
	if !strings.Contains(lSrc, "func (l *LiveTailService) LiveTail(w http.ResponseWriter, r *http.Request) error") {
		t.Errorf("passthrough logic signature mismatch:\n%s", lSrc)
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

func TestGenerateTransportMultipartFromFileField(t *testing.T) {
	pkg := analyzePkg(t, multipartSampleDSL)
	root := t.TempDir()
	cfg := sampleConfig()
	if err := GenerateTransport(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	handler, _ := os.ReadFile(filepath.Join(root, "internal/transport/upload-service/upload.go"))
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

// TestGenerateTransportRejectsBadQueryShapes pins the codegen-time
// rejection of unsupported query-binding shapes. Before this gate
// existed, struct/[]struct/map fields on a GET request were silently
// dropped - the handler omitted the bind line and the field landed
// at the logic layer zero-valued, with no error to chase.
//
// Non-string `@path` / `@header` / `@cookie` is enforced earlier by
// the semantic analyser (see `binding/type` diagnostic) so those
// cases live in semantic tests, not here.
//
// Each case constructs a request type that exercises one rejection branch:
//   - Filter Point      → struct on @query
//   - Tags  []Point     → []struct on @query
//   - Meta  map<string,string> → map on @query
//   - Page  Page<Book>  → generic on @query
//   - opt   int? @query  → optional numeric on @query (no clean v1 idiom)
func TestGenerateTransportRejectsBadQueryShapes(t *testing.T) {
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
			err := GenerateTransport(pkg, sampleConfig(), t.TempDir())
			if err == nil {
				t.Fatalf("expected rejection, got nil error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q missing %q", err.Error(), tc.want)
			}
		})
	}
}
