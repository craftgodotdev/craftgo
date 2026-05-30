package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
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
        request   GetUserReq
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

// ---------- handler ----------

func TestGenerateTransportAllVerbs(t *testing.T) {
	pkg := analyze(t, handlerSampleDSL)
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
	if strings.Contains(string(getSrc), "server.JSON().Decode(r.Body") {
		t.Errorf("GET handler must not decode body:\n%s", getSrc)
	}
	// POST handler must decode body via the swappable codec.
	postSrc, _ := os.ReadFile(filepath.Join(dir, "update-user.go"))
	if !strings.Contains(string(postSrc), "server.JSON().Decode(r.Body") {
		t.Errorf("POST handler must decode body via server.JSON:\n%s", postSrc)
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

// TestGenerateTransportSuccessStatus pins the verb-aware success status.
// A POST that returns a body defaults to 201 Created (written before the
// body is encoded); GET/PUT keep the implicit 200 (no explicit
// WriteHeader — the encoder already produces 200); a bodiless method is
// 204; and @status(N) overrides the verb default.
func TestGenerateTransportSuccessStatus(t *testing.T) {
	src := `package design
type Req { id string }
type Res { id string }
@prefix("/api")
service S {
    post Create /things {
        request  Req
        response Res
    }
    get Get /things/{id} {
        request  Req
        response Res
    }
    put Replace /things/{id} {
        request  Req
        response Res
    }
    @status(202)
    post Enqueue /things/enqueue {
        request  Req
        response Res
    }
    delete Remove /things/{id} {
        request  Req
    }
}`
	pkg := analyze(t, src)
	root := t.TempDir()
	if err := GenerateTransport(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	read := func(fn string) string {
		out, err := os.ReadFile(filepath.Join(root, "internal/transport/s", fn))
		if err != nil {
			t.Fatalf("missing %s: %v", fn, err)
		}
		mustParseGo(t, string(out))
		return string(out)
	}

	// POST + body → 201 Created, written before the body encode.
	create := read("create.go")
	if !strings.Contains(create, "w.WriteHeader(http.StatusCreated)") {
		t.Errorf("POST should write 201:\n%s", create)
	}
	if i, j := strings.Index(create, "WriteHeader(http.StatusCreated)"), strings.Index(create, "Encode(w, resp)"); i < 0 || j < 0 || i > j {
		t.Errorf("201 must be written before the body encode:\n%s", create)
	}

	// GET / PUT + body → implicit 200, no explicit WriteHeader.
	for _, fn := range []string{"get.go", "replace.go"} {
		body := read(fn)
		if strings.Contains(body, "w.WriteHeader(") {
			t.Errorf("%s should not write an explicit status (implicit 200):\n%s", fn, body)
		}
	}

	// @status(202) overrides the POST default.
	if enqueue := read("enqueue.go"); !strings.Contains(enqueue, "w.WriteHeader(http.StatusAccepted)") {
		t.Errorf("@status(202) should write 202:\n%s", enqueue)
	}

	// No response body → 204 No Content regardless of verb.
	if remove := read("remove.go"); !strings.Contains(remove, "w.WriteHeader(http.StatusNoContent)") {
		t.Errorf("bodiless handler should write 204:\n%s", remove)
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
	pkg := analyze(t, src)
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
	mustContainAll(t, body,
		`__d := "anon"`,
		`req.Name = &__d`,
		`__d := 20`,
		`req.Limit = &__d`,
		`__d := 0.5`,
		`req.Ratio = &__d`,
		`__d := true`,
		`req.Active = &__d`,
	)
	// The non-defaulted `plain` field must NOT receive an assignment.
	if strings.Contains(body, "req.Plain =") {
		t.Errorf("plain field shouldn't be pre-filled:\n%s", body)
	}
	// Pre-fill must precede JSON decode so absent body fields keep
	// their default.
	if dec := strings.Index(body, "server.JSON().Decode"); dec >= 0 {
		if pre := body[:dec]; !strings.Contains(pre, `__d := "anon"`) {
			t.Error("expected default assignments before body decode")
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
scalar Cents int @gte(0) @lte(1000000)

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
	pkg := analyze(t, src)
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
	mustContainAll(t, got,
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
	)
}

// TestCollectRequestFieldImports covers the cross-package import
// walk for request type fields. A request with a `shared.ID` field
// auto-promoted to @path emits `req.ID = shared.ID(...)` in the
// handler — without scanning field types for cross-pkg refs the
// `shared` import never lands and the handler fails to compile.
func TestCollectRequestFieldImports(t *testing.T) {
	method := &ast.Method{
		Request: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"UserRef"}}},
	}
	pkg := &semantic.Package{
		Name: "services",
		Types: map[string]*ast.TypeDecl{
			"UserRef": {
				Name: "UserRef",
				Body: []ast.TypeMember{
					&ast.Field{Name: "id", Type: &ast.TypeRef{Named: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"shared", "ID"}}}}},
				},
			},
		},
	}
	cross := CrossPkg{"shared": "github.com/example/svc/internal/types/shared"}
	got := collectRequestFieldImports(method, pkg, cross)
	if got["shared"] != "github.com/example/svc/internal/types/shared" {
		t.Errorf("expected `shared` import in collected map, got %v", got)
	}
}

// TestGenerateTransportNamedBindingArg covers the explicit-name
// override on every binding decorator. `@path("user_id")` makes the
// runtime call `r.PathValue("user_id")` instead of the field's Go
// name; same for `@header("X-API-Key")` etc. Without honouring the
// arg, `/users/{user_id}` never binds because `r.PathValue("userId")`
// returns the empty string and the call site never realises the param
// went missing.
func TestGenerateTransportNamedBindingArg(t *testing.T) {
	src := `package design

type GetReq {
    userId  string @path("user_id")
    apiKey  string @header("X-API-Key")
    session string @cookie("session_id")
    sortBy  string? @query("sort_by")
}

service S {
    get Get /users/{user_id} { request GetReq }
}`
	pkg := analyze(t, src)
	root := t.TempDir()
	if err := GenerateTransport(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, "internal/transport/s/get.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	mustParseGo(t, got)
	mustContainAll(t, got,
		`r.PathValue("user_id")`,
		`r.Header.Get("X-API-Key")`,
		`r.Cookie("session_id")`,
		`r.URL.Query().Get("sort_by")`,
	)
}

// TestGenerateTransportWireNumericAcrossSources locks the Round-2.5
// unification: numeric / bool fields can ride @query, @header,
// @cookie, AND @form through the same parse + 400 idiom. The only
// difference between bindings is the source extraction (Query().Get
// vs Header.Get vs c.Value vs FormValue) — everything else (parse
// call, cast, error path) is shared by [renderWireBindLine].
//
// Without this fix `int @header` etc. either silently zeroed the
// field at runtime (form) or rejected at semantic time (header /
// cookie). Now they parse, with parse failures returning 400 Bad
// Request like @query already did.
func TestGenerateTransportWireNumericAcrossSources(t *testing.T) {
	src := `package design

scalar Cents int
enum Priority { Low = 1  High = 2 }

type Req {
    qLimit  int      @query
    qFlag   bool     @query
    hCount  int      @header
    hRatio  float64  @header
    cTier   Priority @cookie
    cAge    Cents    @cookie
    fQty    int      @form
    fFlag   bool     @form
    upload  file     @form
}

service S {
    post Run /run {
        request Req
    }
}`
	pkg := analyze(t, src)
	root := t.TempDir()
	if err := GenerateTransport(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, "internal/transport/s/run.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	mustParseGo(t, got)
	mustContainAll(t, got,
		// @query int: parse + cast.
		`if _v := r.URL.Query().Get("qLimit"); _v != ""`,
		`_n, _err := strconv.ParseInt(_v, 10, 64)`,
		`req.QLimit = int(_n)`,
		// @query bool: ParseBool.
		`if _v := r.URL.Query().Get("qFlag"); _v != ""`,
		`_n, _err := strconv.ParseBool(_v)`,
		`req.QFlag = _n`,
		// @header int: same shape, different source.
		`if _v := r.Header.Get("hCount"); _v != ""`,
		`req.HCount = int(_n)`,
		// @header float64: ParseFloat with bit-width.
		`if _v := r.Header.Get("hRatio"); _v != ""`,
		`_n, _err := strconv.ParseFloat(_v, 64)`,
		`req.HRatio = float64(_n)`,
		// @cookie int-enum: wrapped in cookie guard, parse + alias cast.
		// The codegen double-casts (`Alias(int(_n))`) because the cast
		// pipeline runs primitive normalisation before alias wrap; both
		// casts are required to satisfy Go's strict typing for int64
		// → int → Priority.
		`if c, err := r.Cookie("cTier"); err == nil {`,
		`if _v := c.Value; _v != ""`,
		`req.CTier = types.Priority(int(_n))`,
		// @cookie int-scalar: same shape, scalar cast.
		`if c, err := r.Cookie("cAge"); err == nil {`,
		`req.CAge = types.Cents(int(_n))`,
		// @form int: source becomes FormValue, otherwise identical.
		`if _v := r.FormValue("fQty"); _v != ""`,
		`req.FQty = int(_n)`,
		// @form bool: ParseBool through FormValue.
		`if _v := r.FormValue("fFlag"); _v != ""`,
		`req.FFlag = _n`,
		// @form file: bound through r.FormFile.
		`r.FormFile("upload")`,
		// strconv import flows through to the multipart template.
		`"strconv"`,
	)
}

// TestGenerateTransportOptionalHeaderCookie covers the Round-1
// extension that lets `string? @header` and `string? @cookie` bind
// through to `*<T>` cleanly. Missing or empty wire values land the
// field as a nil pointer; present values flow through the alias cast
// (when the field is a typed scalar / enum) and address a new
// alias-typed variable so the pointer carries the field's declared
// type instead of bare `*string`.
func TestGenerateTransportOptionalHeaderCookie(t *testing.T) {
	src := `package design

enum Color { Red  Green  Blue }
scalar Email string @format(email)

type Req {
    auth    string? @header
    contact Email?  @header
    theme   Color?  @cookie
    sid     string? @cookie
}

service S {
    get Lookup /items { request Req }
}`
	pkg := analyze(t, src)
	root := t.TempDir()
	if err := GenerateTransport(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, "internal/transport/s/lookup.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	mustParseGo(t, got)
	mustContainAll(t, got,
		// Plain string header: take address of the raw value directly.
		`if _v := r.Header.Get("auth"); _v != ""`,
		`req.Auth = &_v`,
		// Scalar-typed header: route through alias cast into _w.
		`if _v := r.Header.Get("contact"); _v != ""`,
		`_w := types.Email(_v)`,
		`req.Contact = &_w`,
		// Enum-typed cookie: outer cookie guard, inner non-empty guard,
		// alias cast on c.Value.
		`if c, err := r.Cookie("theme"); err == nil {`,
		`if _v := c.Value; _v != ""`,
		`_w := types.Color(_v)`,
		`req.Theme = &_w`,
		// Plain string cookie: outer cookie guard, inner non-empty
		// guard, take address of inner _v.
		`if c, err := r.Cookie("sid"); err == nil {`,
		`req.Sid = &_v`,
	)
}

// TestGenerateTransportOptionalEnumScalarQuery covers the optional
// alias-typed query binding. A field `sort Color? @query` becomes
// `*Color` in Go; the query string yields a raw `string`, so a naive
// `req.Sort = &_v` is a `*string` and refuses to compile against the
// `*Color` field. The fix routes the raw string through the alias cast
// into a fresh variable and addresses THAT variable.
func TestGenerateTransportOptionalEnumScalarQuery(t *testing.T) {
	src := `package design

enum Color { Red  Green  Blue }
scalar Email string @format(email)

type SearchReq {
    sort  Color?  @query
    cc    Email?  @query
    plain string? @query
}

service S {
    get Search /items { request SearchReq }
}`
	pkg := analyze(t, src)
	root := t.TempDir()
	if err := GenerateTransport(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, "internal/transport/s/search.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	mustParseGo(t, got)
	// Optional enum → cast through alias type and address the new
	// alias-typed variable.
	mustContainAll(t, got,
		`_w := types.Color(_v)`,
		`req.Sort = &_w`,
		`_w := types.Email(_v)`,
		`req.Cc = &_w`,
		// Plain *string still takes the address of the raw value.
		`req.Plain = &_v`,
	)
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
	pkg := analyze(t, src)
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
	mustContainAll(t, body,
		"__d := types.StatusPending",
		"req.St = &__d",
	)
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
	pkg := analyze(t, src)
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
		`w.Header().Set("Content-Type", "application/json; charset=utf-8")`,
	}
	for _, want := range checks {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in handler:\n%s", want, body)
		}
	}
	// Ensure header/cookie writes precede the body encoder so they hit the
	// wire before WriteHeader implicitly fires.
	if idx := strings.Index(body, "server.JSON().Encode"); idx >= 0 {
		pre := body[:idx]
		if !strings.Contains(pre, "w.Header().Set(\"etag\"") {
			t.Error("expected response header write to precede body encode")
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

// TestGenerateTransportResponseHeaderCookieNamedArg pins the
// explicit-name override on the response side. `@header("X-Y-Z")` /
// `@cookie("session_id")` must drive the wire name, not the Go field
// name - identical to the request-side behaviour. Without this fix
// `count string @header("X-Total-Count")` emitted
// `w.Header().Set("count", ...)`, losing the canonical HTTP name.
func TestGenerateTransportResponseHeaderCookieNamedArg(t *testing.T) {
	src := `package design
type ListReq { q string @query }
type ListResp {
    items     string
    total     string @header("X-Total-Count")
    sessionID string @cookie("session_id")
}
service Catalog {
    get List /items {
        request   ListReq
        response  ListResp
    }
}`
	pkg := analyze(t, src)
	root := t.TempDir()
	if err := GenerateTransport(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(root, "internal/transport/catalog/list.go"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(out)
	mustParseGo(t, body)
	mustContainAll(t, body,
		`w.Header().Set("X-Total-Count", resp.Total)`,
		`http.SetCookie(w, &http.Cookie{Name: "session_id", Value: resp.SessionID})`,
	)
	// Negative: the Go field name must NOT appear as the wire name.
	for _, banned := range []string{
		`w.Header().Set("total"`,
		`Cookie{Name: "sessionID"`,
	} {
		if strings.Contains(body, banned) {
			t.Errorf("explicit arg was ignored - found %q in:\n%s", banned, body)
		}
	}
}

func TestGenerateTypesNonBodyBindingsAreSkipped(t *testing.T) {
	pkg := analyze(t, `package design
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
	pkg := analyze(t, handlerSampleDSL)
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
	mustContainAll(t, string(out),
		"WriteResponseHeaders(http.ResponseWriter)",
		"hw.WriteResponseHeaders(w)",
	)
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
	pkg := analyze(t, handlerSampleDSL)
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
// `@middlewares(S)`, the generated `srv.Handle` call lists service-
// level middlewares first (outermost) followed by the method's, in
// source order. Server.Handle wraps variadic middlewares right-to-left
// so the first arg ends up the outermost frame at runtime — the route
// line reads top-to-bottom the same way the request flows through
// (Auth wraps RateLimit wraps RequestCounter wraps the handler).
func TestGenerateRoutesMultipleMiddlewares(t *testing.T) {
	pkg := analyze(t, `package design

type Thing { id string }
type GetThingReq { id string @path }

middleware Auth
middleware RateLimit
middleware RequestCounter

@prefix("/v1")
@middlewares(Auth)
service S {
    @middlewares(RateLimit, RequestCounter)
    get GetThing /things/{id} {
        request  GetThingReq
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
	want := `srv.Handle("GET /v1/v1/things/{id}", transport.GetThing(svcCtx), svcCtx.Auth, svcCtx.RateLimit, svcCtx.RequestCounter)`
	if !strings.Contains(src, want) {
		t.Errorf("expected variadic middleware chain %q in:\n%s", want, src)
	}
}

// TestGenerateRoutesIgnoreMiddlewareClearsInherited pins the
// `@ignoreMiddleware` opt-out: a method with this decorator must
// NOT see the service-level chain. Combined with a method-level
// `@middlewares(...)` it becomes "reset + replace" - the method
// keeps only its own chain.
func TestGenerateRoutesIgnoreMiddlewareClearsInherited(t *testing.T) {
	pkg := analyze(t, `package design

type Thing { id string }
type GetThingReq { id string @path }

middleware Auth
middleware Audit

@prefix("/v1")
@middlewares(Auth)
service S {
    @ignoreMiddleware
    @middlewares(Audit)
    get GetThing /things/{id} {
        request  GetThingReq
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
	if strings.Contains(src, "svcCtx.Auth") {
		t.Errorf("Auth should be cleared by @ignoreMiddleware:\n%s", src)
	}
	want := `srv.Handle("GET /v1/v1/things/{id}", transport.GetThing(svcCtx), svcCtx.Audit)`
	if !strings.Contains(src, want) {
		t.Errorf("expected method-only chain %q in:\n%s", want, src)
	}
}

// TestGenerateRoutesGroupAddsPathSegment confirms `@group("...")` on a
// service stitches its argument into the route between the @prefix and
// the method path. A service with `@prefix("/v1") @group("admin")`
// produces `/<basePath>/v1/admin/<method-path>`.
func TestGenerateRoutesGroupAddsPathSegment(t *testing.T) {
	pkg := analyze(t, `package design

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
	mustContainAll(t, src,
		`"GET /api/v1/admin/things"`,
		`"GET /api/v1/admin/health"`,
	)
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
	pkg := analyze(t, src)
	root := t.TempDir()
	if err := GenerateRoutes(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(root, "internal/routes/s/routes.go"))
	body := string(out)
	mustParseGo(t, body)
	mustContainAll(t, body,
		`"time"`,
		"server.WithLimits",
		"Timeout: 500 * time.Millisecond",
		"MaxBodySize: 1024",
	)
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
	pkg := analyze(t, src)
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
	pkg := analyze(t, `package design

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
	pkg := analyze(t, handlerSampleDSL)
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

func TestGenerateServiceGenericInstantiation(t *testing.T) {
	// `response Page<User>` must render generic args inline as
	// `*types.Page[types.User]`. A bare `*types.Page` would fail to
	// compile with "cannot use generic type Page[T any] without
	// instantiation". Local type args pick up the canonical `types.`
	// alias; scalar args, multi-arg generics, and nested
	// instantiations flow through the same path.
	src := `package design
type User { id string }
scalar Email string @format(email)
type Page<T> { items T[]  total int }
type Envelope<T> { data T }
type Pair<A, B> { left A  right B }
type CreateReq { user User }
type EchoReq { v string }
type WrapReq { v string }
type PairReq { v string }
service S {
    post Create /c   { request CreateReq  response Page<User> }
    post Echo   /e   { request EchoReq    response Envelope<Email> }
    post Wrap   /w   { request WrapReq    response Page<Envelope<User>> }
    post Pair   /p   { request PairReq    response Pair<User, Email> }
    post Mix    /m   { request Page<User> response Envelope<User> }
}`
	pkg := analyze(t, src)
	root := t.TempDir()
	if err := GenerateService(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		file, want string
	}{
		// Single-arg generic with local type.
		{"create.go", "(*types.Page[types.User], error)"},
		// Single-arg generic with local scalar.
		{"echo.go", "(*types.Envelope[types.Email], error)"},
		// Nested generic.
		{"wrap.go", "(*types.Page[types.Envelope[types.User]], error)"},
		// Multi-arg generic mixing struct + scalar.
		{"pair.go", "(*types.Pair[types.User, types.Email], error)"},
		// Generic on the REQUEST side too.
		{"mix.go", "(req *types.Page[types.User])"},
		{"mix.go", "(*types.Envelope[types.User], error)"},
	}
	for _, c := range cases {
		body, err := os.ReadFile(filepath.Join(root, "internal/service/s", c.file))
		if err != nil {
			t.Fatalf("read %s: %v", c.file, err)
		}
		got := string(body)
		mustParseGo(t, got)
		if !strings.Contains(got, c.want) {
			t.Errorf("%s missing %q:\n%s", c.file, c.want, got)
		}
	}
}

func TestGenerateServiceSkipsExisting(t *testing.T) {
	pkg := analyze(t, handlerSampleDSL)
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
	pkg := analyze(t, handlerSampleDSL)
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
	pkg := analyze(t, passthroughSampleDSL)
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
	mustContainAll(t, hSrc,
		"l.LiveTail(w, r)",
		"writeError(w, err)",
		"http.HandlerFunc",
	)
	mustContainNone(t, hSrc,
		"server.JSON().Decode",
	)

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
	pkg := analyze(t, multipartSampleDSL)
	root := t.TempDir()
	cfg := sampleConfig()
	if err := GenerateTransport(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	handler, _ := os.ReadFile(filepath.Join(root, "internal/transport/upload-service/upload.go"))
	mustParseGo(t, string(handler))
	mustContainAll(t, string(handler),
		"r.ParseMultipartForm(",
		`r.FormValue("note")`,
		`r.FormFile("avatar")`,
		"req.Avatar = header",
	)
	if strings.Contains(string(handler), "server.JSON().Decode(r.Body") {
		t.Errorf("multipart handler must not JSON-decode body:\n%s", handler)
	}
}

// TestGenerateTransportRejectsBadQueryShapes pins the codegen-time
// rejection of unsupported query-binding shapes. Without the gate,
// struct/[]struct/map fields on a GET request would be silently
// dropped — the handler would omit the bind line and the field
// would land at the logic layer zero-valued, with no error to chase.
//
// Non-string `@path` / `@header` / `@cookie` is enforced at the
// semantic layer (see `binding/type` diagnostic) so those cases
// live in semantic tests, not here.
//
// Each case constructs a request type that exercises one rejection branch:
//   - Filter Point      → struct on @query
//   - Tags  []Point     → []struct on @query
//   - Meta  map<string,string> → map on @query
//   - Page  Page<Book>  → generic on @query
//   - opt   int? @query  → optional numeric on @query (no clean idiom)
//
// These shapes are rejected at SEMANTIC time by
// [semantic.checkBindingFieldType] (see `TestCodeOnBindingType` in
// internal/semantic/decorators_test.go) so they never reach codegen.
// The codegen layer keeps a defensive rejection in
// [renderWireBindLine] for direct AST callers that skip semantic, but
// the design-time test is the authoritative coverage and lives next
// to the rule.
