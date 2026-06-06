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

// TestGenerateTransportDefaults pins the @default pre-fill emission. The
// handler assigns each declared default BEFORE the JSON decode so
// fields absent from the body keep the DSL value; explicit fields in
// the body still overwrite via the standard decoder semantics.
func TestGenerateTransportDefaults(t *testing.T) {
	src := `package design
type Req {
    name string?  @default("anon")
    limit int?     @default(20)
    ratio float64? @default(0.5)
    active bool?    @default(true)
    width  int32?   @default(7)
    wide   int64?   @default(9)
    ucount uint16?  @default(3)
    pct    float32? @default(1.5)
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
	// A narrow numeric width casts the literal to the field's primitive so
	// the pointer pre-fill `__d := int32(7)` keeps the field's `*int32`;
	// without the cast `__d := 7` is `*int` and `&__d` fails to assign.
	// `int` / `float64` above stay BARE (the literal already matches).
	mustContainAll(t, body,
		`__d := int32(7)`,
		`req.Width = &__d`,
		`__d := int64(9)`,
		`req.Wide = &__d`,
		`__d := uint16(3)`,
		`req.Ucount = &__d`,
		`__d := float32(1.5)`,
		`req.Pct = &__d`,
	)
	// Guard the no-needless-cast contract: int / float64 literals carry
	// no cast (would be `int(20)` / `float64(0.5)` if over-applied).
	if strings.Contains(body, "int(20)") || strings.Contains(body, "float64(0.5)") {
		t.Errorf("int / float64 defaults must not be cast:\n%s", body)
	}
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
		`req.Contact = types.Email(_q.Get("contact"))`,
		// query int-backed enum: bind helper parses + converts to the enum
		`server.BindValue(w, r, "priority", "int", _q.Get("priority"), &req.Priority, server.ParseSigned[types.Priority])`,
		// query numeric scalar: bind helper parses + converts to the scalar
		`server.BindValue(w, r, "cap", "int", _q.Get("cap"), &req.Cap, server.ParseSigned[types.Cents])`,
		// cookie cast
		`req.Sess = c.Value`,
		// header cast (string-backed enum)
		`req.Role = types.Status(r.Header.Get("role"))`,
	)
}

// TestGenerateTransportEnumArrayQueryDefault pins the enum-array
// @query @default binder. The slice is pre-filled with the default
// members' wire values; a present param must REPLACE that pre-fill,
// not append to it — the `req.X = req.X[:0]` clear is what guards
// against `?colors=green` yielding `[red green blue]` instead of
// `[green]`. The has-default oracle the binder consults resolves the
// enum-member array literal so it agrees with the pre-fill emission.
func TestGenerateTransportEnumArrayQueryDefault(t *testing.T) {
	src := `package design

enum Color { Red  Green  Blue }

type Req { colors Color[]? @query @default([Red, Blue]) }

service S {
    get Get /thing { request Req }
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
		// pre-fill with the default members' wire values
		`req.Colors = []types.Color{types.ColorRed, types.ColorBlue}`,
		// present param REPLACES the pre-fill: clear, then append
		`req.Colors = req.Colors[:0]`,
		`req.Colors = append(req.Colors, types.Color(_v))`,
	)
}

// TestGenerateTransportWholeNumberFloatDefault pins that a whole-number
// float `@default(1.0)` renders as a float literal (`1.0`), not `1` — which
// would infer `int` and make the optional-field pointer pre-fill `*int`
// instead of `*float64`. A fractional default (`2.5`) is unchanged and
// carries no cast.
func TestGenerateTransportWholeNumberFloatDefault(t *testing.T) {
	src := `package design

type Req {
    x float64? @default(1.0)
    y float64? @default(2.5)
}

service S {
    post Do /do { request Req }
}`
	pkg := analyze(t, src)
	root := t.TempDir()
	if err := GenerateTransport(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, "internal/transport/s/do.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	mustParseGo(t, got)
	mustContainAll(t, got, `__d := 1.0`, `__d := 2.5`)
	if strings.Contains(got, "__d := 1\n") || strings.Contains(got, "float64(1)") {
		t.Errorf("whole-number float default must render as float literal `1.0`, not `1` / `float64(1)`:\n%s", got)
	}
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
	got := collectRequestFieldImports(method, pkg, cross, nil)
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
		// The query map is parsed once into _q.
		`_q := r.URL.Query()`,
		// @query("sort_by"): _q is read by the WIRE name, not the Go
		// field name `sortBy`.
		`_q.Get("sort_by")`,
	)
	// No per-field r.URL.Query() calls, and the field name never leaks
	// in as the query key.
	mustContainNone(t, got, `r.URL.Query().Get(`, `_q.Get("sortBy")`)
}

// TestGenerateTransportWireNumericAcrossSources covers the unified
// wire binding: numeric / bool fields can ride @query, @header,
// @cookie, AND @form through the same parse + 400 idiom. The only
// difference between bindings is the source extraction (Query().Get
// vs Header.Get vs c.Value vs FormValue) — everything else (parse
// call, cast, error path) is shared by [renderWireBindLine].
//
// `int @header` etc. parse the wire value, with parse failures
// returning 400 Bad Request the same way @query does.
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
		// @query int → BindValue + ParseSigned[int].
		`server.BindValue(w, r, "qLimit", "int", _q.Get("qLimit"), &req.QLimit, server.ParseSigned[int])`,
		// @query bool → ParseBool.
		`server.BindValue(w, r, "qFlag", "bool", _q.Get("qFlag"), &req.QFlag, server.ParseBool[bool])`,
		// @header int: same helper, different source.
		`server.BindValue(w, r, "hCount", "int", r.Header.Get("hCount"), &req.HCount, server.ParseSigned[int])`,
		// @header float64 → ParseFloat[float64] (the helper picks the bit width).
		`server.BindValue(w, r, "hRatio", "float", r.Header.Get("hRatio"), &req.HRatio, server.ParseFloat[float64])`,
		// @cookie int-enum: wrapped in the cookie guard, parses c.Value.
		`if c, err := r.Cookie("cTier"); err == nil {`,
		`server.BindValue(w, r, "cTier", "int", c.Value, &req.CTier, server.ParseSigned[types.Priority])`,
		// @cookie int-scalar: same shape, scalar type argument.
		`if c, err := r.Cookie("cAge"); err == nil {`,
		`server.BindValue(w, r, "cAge", "int", c.Value, &req.CAge, server.ParseSigned[types.Cents])`,
		// @form int: source becomes FormValue.
		`server.BindValue(w, r, "fQty", "int", r.FormValue("fQty"), &req.FQty, server.ParseSigned[int])`,
		// @form bool.
		`server.BindValue(w, r, "fFlag", "bool", r.FormValue("fFlag"), &req.FFlag, server.ParseBool[bool])`,
		// @form file: bound through r.FormFile.
		`r.FormFile("upload")`,
	)
	// Parsing lives in the helpers now, so the handler no longer imports strconv.
	mustContainNone(t, got, `"strconv"`)
}

// TestGenerateTransportOptionalHeaderCookie covers `string? @header`
// and `string? @cookie` binding through to `*<T>`. Missing or empty
// wire values land the field as a nil pointer; present values flow
// through the alias cast (when the field is a typed scalar / enum) and
// address a new alias-typed variable so the pointer carries the
// field's declared type instead of bare `*string`.
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
// `*Color` field. The binder routes the raw string through the alias
// cast into a fresh variable and addresses THAT variable.
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
// `@cookie("session_id")` drive the wire name, not the Go field name -
// identical to the request-side behaviour. So
// `count string @header("X-Total-Count")` emits
// `w.Header().Set("X-Total-Count", ...)`, keeping the canonical HTTP
// name.
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

// TestGenerateTransportResponseHeaderNonString pins the non-string
// response @header / @cookie formatting: int / float / bool / enum
// values are rendered to their wire string via strconv, optional
// headers are nil-guarded, and array headers emit one Header().Add per
// element. Plain strings still pass through untouched.
func TestGenerateTransportResponseHeaderNonString(t *testing.T) {
	src := `package design
enum Tier { Free = "free"  Pro = "pro" }
scalar Cents int
scalar SKU   string
type StatsResp {
    items    string
    count    int      @header("X-Total-Count")
    ratio    float64  @header("X-Ratio")
    tier     Tier     @header("X-Tier")
    price    Cents    @header("X-Price")
    sku      SKU      @header("X-SKU")
    nextPage string?  @header("X-Next-Page")
    labels   string[] @header("X-Label")
    active   bool     @cookie("flag")
    plain    string   @cookie("plain")
}
service S {
    get Stats /stats { response StatsResp }
}`
	pkg := analyze(t, src)
	root := t.TempDir()
	if err := GenerateTransport(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(root, "internal/transport/s/stats.go"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(out)
	mustParseGo(t, body)
	mustContainAll(t, body,
		`"strconv"`,
		`w.Header().Set("X-Total-Count", strconv.Itoa(resp.Count))`,
		`w.Header().Set("X-Ratio", strconv.FormatFloat(resp.Ratio, 'g', -1, 64))`,
		`w.Header().Set("X-Tier", string(resp.Tier))`,
		// Numeric scalar → cast to int64 then strconv; string scalar →
		// string() conversion.
		`w.Header().Set("X-Price", strconv.FormatInt(int64(resp.Price), 10))`,
		`w.Header().Set("X-SKU", string(resp.Sku))`,
		`if resp.NextPage != nil {`,
		`w.Header().Set("X-Next-Page", *resp.NextPage)`,
		`for _, _v := range resp.Labels {`,
		`w.Header().Add("X-Label", _v)`,
		`http.SetCookie(w, &http.Cookie{Name: "flag", Value: strconv.FormatBool(resp.Active)})`,
		// Plain string still writes the value directly (no conversion).
		`http.SetCookie(w, &http.Cookie{Name: "plain", Value: resp.Plain})`,
	)
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

// TestGenerateGroupNestsOutputNotRoute confirms `@group("admin/ops")` on a
// service nests its generated transport handlers (and the errors helper) under
// <transport>/<service>/<group>/ and points the route file's transport import
// at that nested package - while the route pattern and OpenAPI path stay free
// of the group. The route file itself stays flat at routes/<service>/.
func TestGenerateGroupNestsOutputNotRoute(t *testing.T) {
	pkg := analyze(t, `package design

type Thing { id string }

@prefix("/v1")
@group("admin/ops")
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
	if err := GenerateTransport(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}

	// Route pattern carries no group, but the routes file itself lives in the
	// group folder (the @group replaces the service name on disk, just like the
	// transport handlers) and imports the group transport package.
	out, _ := os.ReadFile(filepath.Join(root, "internal/routes/admin/ops/routes.go"))
	src := string(out)
	mustParseGo(t, src)
	mustContainAll(t, src,
		`"GET /api/v1/things"`,
		`"GET /api/v1/health"`,
		`transport/admin/ops"`,
	)
	if _, err := os.Stat(filepath.Join(root, "internal/routes/admin-service")); err == nil {
		t.Error("a fully-grouped service should not emit a service-name routes dir")
	}
	if strings.Contains(src, "transport/admin-service/admin/ops") {
		t.Errorf("@group should replace the service-name segment, not nest under it:\n%s", src)
	}
	if strings.Contains(src, "/admin/ops/things") || strings.Contains(src, "v1/admin/ops") {
		t.Errorf("@group leaked into the route pattern:\n%s", src)
	}

	// Handlers land under the group path (which replaces the service name on
	// disk); error rendering is the framework's server.WriteError, so no
	// per-package errors helper is emitted.
	for _, rel := range []string{
		"internal/transport/admin/ops/list-all.go",
		"internal/transport/admin/ops/health.go",
	} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("expected generated file %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "internal/transport/admin-service")); err == nil {
		t.Error("a fully-grouped service should not emit a service-name transport dir")
	}
}

// TestGenerateExtendGroupNestsPerBlock pins per-block @group: a primary block
// and an extend block each carry their own @group, so the service's methods
// split across two transport packages on disk — and the routes file follows the
// same split, one routes file per group folder, each importing only its own
// group's transport and registering only that group's methods.
func TestGenerateExtendGroupNestsPerBlock(t *testing.T) {
	pkg := analyze(t, `package design

type Thing { id string }

@prefix("/v1")
service Catalog {
    get ListThings /things { response Thing }
}

@group("v2")
extend service Catalog {
    get ListThingsV2 /v2/things { response Thing }
}`)
	root := t.TempDir()
	cfg := sampleConfig()
	if err := GenerateRoutes(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	if err := GenerateTransport(pkg, cfg, root); err != nil {
		t.Fatal(err)
	}

	// Primary method stays at the service directory; the extend block's
	// method lands under its own group (which replaces the service name).
	for _, rel := range []string{
		"internal/transport/catalog/list-things.go",
		"internal/transport/v2/list-things-v2.go",
	} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("expected generated file %s: %v", rel, err)
		}
	}

	// Both handlers render errors through the framework's server.WriteError -
	// no per-package helper, no cross-package transport import.
	grouped, _ := os.ReadFile(filepath.Join(root, "internal/transport/v2/list-things-v2.go"))
	if strings.Contains(string(grouped), "roottransport") {
		t.Error("the grouped handler should not import a root transport package")
	}
	mustContainAll(t, string(grouped), "server.WriteError(w, r, err)")
	if _, err := os.Stat(filepath.Join(root, "internal/transport/v2/errors.go")); err == nil {
		t.Error("no per-package errors.go should be emitted; errors render via server.WriteError")
	}

	// Routes split per group, mirroring transport: the ungrouped primary method
	// has its routes file at the service directory importing the root transport;
	// the @group("v2") method has its own routes file under v2/ importing only
	// the v2 transport. Neither file mentions the other group's package.
	primaryRoutes, _ := os.ReadFile(filepath.Join(root, "internal/routes/catalog/routes.go"))
	psrc := string(primaryRoutes)
	mustParseGo(t, psrc)
	mustContainAll(t, psrc, `transport "`, "transport.ListThings(svcCtx)")
	if strings.Contains(psrc, "ListThingsV2") || strings.Contains(psrc, "transport/v2") {
		t.Errorf("primary routes file must not register the v2 group:\n%s", psrc)
	}

	groupRoutes, _ := os.ReadFile(filepath.Join(root, "internal/routes/v2/routes.go"))
	gsrc := string(groupRoutes)
	mustParseGo(t, gsrc)
	mustContainAll(t, gsrc, `transportV2 "`, "transportV2.ListThingsV2(svcCtx)")
	if strings.Contains(gsrc, "ListThings(svcCtx)") {
		t.Errorf("v2 group routes file must not register the primary method:\n%s", gsrc)
	}
	if _, err := os.Stat(filepath.Join(root, "internal/routes/catalog-v2")); err == nil {
		t.Error("group routes should nest at the group segment, not a service-name variant")
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
		"server.WriteError(w, r, err)",
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

// TestGenerateTransportFormExplicitWireName pins that an explicit
// @form("wire_name") sets the runtime r.FormFile / r.FormValue key, not
// the Go field name, so a client posting under the declared wire name
// binds.
func TestGenerateTransportFormExplicitWireName(t *testing.T) {
	pkg := analyze(t, `package design
type UploadReq {
    caption  string  @form("note_text")
    pic      file    @form("avatar_file")
}
type UploadResp { ok bool }
service UploadService {
    post Upload /upload { request UploadReq  response UploadResp }
}`)
	root := t.TempDir()
	if err := GenerateTransport(pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	handler, _ := os.ReadFile(filepath.Join(root, "internal/transport/upload-service/upload.go"))
	mustParseGo(t, string(handler))
	mustContainAll(t, string(handler),
		`r.FormValue("note_text")`,
		`r.FormFile("avatar_file")`,
	)
	// The Go field names must NOT leak through as the form keys.
	if strings.Contains(string(handler), `r.FormValue("caption")`) || strings.Contains(string(handler), `r.FormFile("pic")`) {
		t.Errorf("explicit @form name ignored — form key fell back to the field name:\n%s", handler)
	}
}

// TestRequestFieldsNestedCrossPkgMixin pins that flattenFields collects a
// field reached through a mixin nested INSIDE a cross-package mixin
// (app.Req -> shared.Outer -> shared.Inner). The bare inner `Inner` must
// be qualified as `shared.Inner`, or its field silently drops from the
// binder / default pre-fill while OpenAPI (built from a merged package)
// still advertises it.
func TestRequestFieldsNestedCrossPkgMixin(t *testing.T) {
	root, files := projectFiles(t, map[string]string{
		"shared/types.craftgo": `package shared
type Inner { deep int32? @default(7) }
type Outer { Inner  mid int64? @default(9) }`,
		"app/types.craftgo": `package app
import "shared"
type Req { shared.Outer  own string }`,
	})
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("semantic: %v", diags)
	}
	appPkg := proj.Packages["app"]
	if appPkg == nil {
		t.Fatal("app package missing")
	}
	r := BuildProjectResolver(proj, newFixtureConfig(), "app")
	got := map[string]bool{}
	var names []string
	for _, f := range requestFields(appPkg.Types["Req"], appPkg, r) {
		got[f.Name] = true
		names = append(names, f.Name)
	}
	// own = direct, mid = one cross-pkg level, deep = two levels (nested).
	for _, want := range []string{"own", "mid", "deep"} {
		if !got[want] {
			t.Errorf("requestFields dropped %q (nested cross-pkg mixin field); collected %v", want, names)
		}
	}
}

// TestResolveRequestFieldsQualifiedRequestNestedMixin pins that a QUALIFIED
// request type (`request shared.Holder`) has its bare nested mixin's fields
// resolved and bound. The request type lives in `shared`, so its bare mixin
// `Sub` resolves there — without threading that package as the flatten
// prefix, `q` and `bod` were silently dropped from the binder while the
// validator and the semantic path-param check still enforced them.
func TestResolveRequestFieldsQualifiedRequestNestedMixin(t *testing.T) {
	root, files := projectFiles(t, map[string]string{
		"shared/types.craftgo": `package shared
type Sub { q string @query @length(2, 5)  bod string @length(1, 10) }
type Holder { Sub  id string @path }`,
		"app/types.craftgo": `package app
import "shared"
type Resp { ok bool }
service S { post DoIt /h/{id} { request shared.Holder  response Resp } }`,
	})
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("semantic: %v", diags)
	}
	appPkg := proj.Packages["app"]
	if appPkg == nil {
		t.Fatal("app package missing")
	}
	r := BuildProjectResolver(proj, newFixtureConfig(), "app")
	m := &ast.Method{
		Verb:    "post",
		Request: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"shared", "Holder"}}},
		Path:    &ast.Path{Segments: []*ast.PathSegment{{Literal: "h"}, {Param: true, Literal: "id"}}},
	}
	got := map[string]Binding{}
	var names []string
	for _, rf := range resolveRequestFields(m, appPkg, r) {
		got[rf.DSLName] = rf.Binding
		names = append(names, rf.DSLName)
	}
	if got["q"] != BindQuery {
		t.Errorf("q should bind @query (from the cross-pkg request's bare mixin); got %v, fields %v", got["q"], names)
	}
	if _, ok := got["bod"]; !ok {
		t.Errorf("bod (cross-pkg request body field) dropped; fields %v", names)
	}
	if got["id"] != BindPath {
		t.Errorf("id should bind @path; got %v, fields %v", got["id"], names)
	}
}
