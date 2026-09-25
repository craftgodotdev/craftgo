package golang

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/route"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/wire"
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
			Wiring:     "./internal/wiring",
			OpenAPI:    "./docs/openapi.yaml",
			FileCase:   idents.FileCaseKebab,
		},
		OpenAPI: config.OpenAPI{BasePath: "/v1"},
	}
}

// ---------- handler ----------

// Every verb's handler parses; only a body verb decodes the body, and a bodiless method writes 204.
func TestGenerateTransportAllVerbs(t *testing.T) {
	pkg := analyze(t, handlerSampleDSL)
	root := t.TempDir()
	cfg := sampleConfig()
	if err := generateTransport(pkg, cfg, root, nil); err != nil {
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

	getSrc, _ := os.ReadFile(filepath.Join(dir, "get-user.go"))
	if strings.Contains(string(getSrc), "server.JSON().Decode(r.Body") {
		t.Errorf("GET handler must not decode body:\n%s", getSrc)
	}
	postSrc, _ := os.ReadFile(filepath.Join(dir, "update-user.go"))
	if !strings.Contains(string(postSrc), "server.JSON().Decode(r.Body") {
		t.Errorf("POST handler must decode body via server.JSON:\n%s", postSrc)
	}
	pingSrc, _ := os.ReadFile(filepath.Join(dir, "ping.go"))
	if !strings.Contains(string(pingSrc), "l.Ping()") {
		t.Errorf("Ping handler should call l.Ping() with no arg:\n%s", pingSrc)
	}
	if !strings.Contains(string(pingSrc), "http.StatusNoContent") {
		t.Errorf("Ping handler should write 204:\n%s", pingSrc)
	}
}

// A POST with a body writes 201 before encoding, GET and PUT write no explicit status, a bodiless
// method writes 204, and @status overrides the verb default.
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
	if err := generateTransport(pkg, sampleConfig(), root, nil); err != nil {
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

	create := read("create.go")
	if !strings.Contains(create, "w.WriteHeader(http.StatusCreated)") {
		t.Errorf("POST should write 201:\n%s", create)
	}
	if i, j := strings.Index(create, "WriteHeader(http.StatusCreated)"), strings.Index(create, "Encode(w, resp)"); i < 0 || j < 0 || i > j {
		t.Errorf("201 must be written before the body encode:\n%s", create)
	}

	for _, fn := range []string{"get.go", "replace.go"} {
		body := read(fn)
		if strings.Contains(body, "w.WriteHeader(") {
			t.Errorf("%s should not write an explicit status (implicit 200):\n%s", fn, body)
		}
	}

	if enqueue := read("enqueue.go"); !strings.Contains(enqueue, "w.WriteHeader(http.StatusAccepted)") {
		t.Errorf("@status(202) should write 202:\n%s", enqueue)
	}

	if remove := read("remove.go"); !strings.Contains(remove, "w.WriteHeader(http.StatusNoContent)") {
		t.Errorf("bodiless handler should write 204:\n%s", remove)
	}
}

// Each @default is assigned before the JSON decode, so only a field absent from the body keeps it.
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
	if err := generateTransport(pkg, sampleConfig(), root, nil); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(root, "internal/transport/s/make.go"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(out)
	mustParseGo(t, body)
	// An optional field's default is pre-filled through a pointer.
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
	// A narrow numeric default is cast so `&__d` has the field's pointer type.
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
	if strings.Contains(body, "int(20)") || strings.Contains(body, "float64(0.5)") {
		t.Errorf("int / float64 defaults must not be cast:\n%s", body)
	}
	if strings.Contains(body, "req.Plain =") {
		t.Errorf("plain field shouldn't be pre-filled:\n%s", body)
	}
	if dec := strings.Index(body, "server.JSON().Decode"); dec >= 0 {
		if pre := body[:dec]; !strings.Contains(pre, `__d := "anon"`) {
			t.Error("expected default assignments before body decode")
		}
	}
}

// A non-optional field's @default is assigned to the value itself, cast to
// its scalar, before the JSON decode.
func TestGenerateTransportNonOptionalDefault(t *testing.T) {
	src := `package design
scalar Cents int @gte(0)
type Req {
    limit Cents @default(20)
    plain string
}
service S {
    post Make /make { request Req }
}`
	pkg := analyze(t, src)
	root := t.TempDir()
	if err := generateTransport(pkg, sampleConfig(), root, nil); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(root, "internal/transport/s/make.go"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(out)
	mustParseGo(t, body)
	const prefill = "req.Limit = types.Cents(20)"
	dec := strings.Index(body, "server.JSON().Decode")
	if pre := strings.Index(body, prefill); pre < 0 || dec < 0 || pre > dec {
		t.Errorf("want %q before the body decode:\n%s", prefill, body)
	}
	if strings.Contains(body, "&__d") {
		t.Errorf("a non-optional default is pre-filled through a pointer:\n%s", body)
	}
}

// A bound enum or scalar field is converted from the wire string to its own type.
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
	if err := generateTransport(pkg, sampleConfig(), root, nil); err != nil {
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
		// query: string-backed scalar cast
		`req.Contact = types.Email(_q.Get("contact"))`,
		// query: int-backed enum, parsed by the bind helper
		`server.BindValue(w, r, "priority", "int", _q.Get("priority"), &req.Priority, server.ParseSigned[types.Priority])`,
		// query: numeric scalar, parsed by the bind helper
		`server.BindValue(w, r, "cap", "int", _q.Get("cap"), &req.Cap, server.ParseSigned[types.Cents])`,
		// cookie: plain string
		`req.Sess = c.Value`,
		// header: string-backed enum cast
		`req.Role = types.Status(r.Header.Get("role"))`,
	)
}

// A present enum-array query param replaces its @default pre-fill.
func TestGenerateTransportEnumArrayQueryDefault(t *testing.T) {
	src := `package design

enum Color { Red  Green  Blue }

type Req { colors Color[]? @query @default([Red, Blue]) }

service S {
    get Get /thing { request Req }
}`
	pkg := analyze(t, src)
	root := t.TempDir()
	if err := generateTransport(pkg, sampleConfig(), root, nil); err != nil {
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
		// a present param replaces the pre-fill: clear, then append
		`req.Colors = req.Colors[:0]`,
		`req.Colors = append(req.Colors, types.Color(_v))`,
	)
}

// A whole-number float @default renders as 1.0, so its pre-fill pointer stays *float64.
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
	if err := generateTransport(pkg, sampleConfig(), root, nil); err != nil {
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

// A binding decorator's name argument is the wire key the handler reads.
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
	if err := generateTransport(pkg, sampleConfig(), root, nil); err != nil {
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
		`_q.Get("sort_by")`,
	)
	mustContainNone(t, got, `r.URL.Query().Get(`, `_q.Get("sortBy")`)
}

// Numeric and bool @query/@header/@cookie/@form fields all bind through server.BindValue.
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
	if err := generateTransport(pkg, sampleConfig(), root, nil); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, "internal/transport/s/run.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	mustParseGo(t, got)
	mustContainAll(t, got,
		`server.BindValue(w, r, "qLimit", "int", _q.Get("qLimit"), &req.QLimit, server.ParseSigned[int])`,
		`server.BindValue(w, r, "qFlag", "bool", _q.Get("qFlag"), &req.QFlag, server.ParseBool[bool])`,
		`server.BindValue(w, r, "hCount", "int", r.Header.Get("hCount"), &req.HCount, server.ParseSigned[int])`,
		`server.BindValue(w, r, "hRatio", "float", r.Header.Get("hRatio"), &req.HRatio, server.ParseFloat[float64])`,
		`if c, err := r.Cookie("cTier"); err == nil {`,
		`server.BindValue(w, r, "cTier", "int", c.Value, &req.CTier, server.ParseSigned[types.Priority])`,
		`if c, err := r.Cookie("cAge"); err == nil {`,
		`server.BindValue(w, r, "cAge", "int", c.Value, &req.CAge, server.ParseSigned[types.Cents])`,
		`server.BindValue(w, r, "fQty", "int", r.FormValue("fQty"), &req.FQty, server.ParseSigned[int])`,
		`server.BindValue(w, r, "fFlag", "bool", r.FormValue("fFlag"), &req.FFlag, server.ParseBool[bool])`,
		`r.FormFile("upload")`,
	)
	// Parsing lives in the server helpers, so the handler imports no strconv.
	mustContainNone(t, got, `"strconv"`)
}

// An optional header or cookie field is nil when empty, else points at the value cast to its type.
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
	if err := generateTransport(pkg, sampleConfig(), root, nil); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, "internal/transport/s/lookup.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	mustParseGo(t, got)
	mustContainAll(t, got,
		// plain string header: address of the raw value
		`if _v := r.Header.Get("auth"); _v != ""`,
		`req.Auth = &_v`,
		// scalar header: cast into _w
		`if _v := r.Header.Get("contact"); _v != ""`,
		`_w := types.Email(_v)`,
		`req.Contact = &_w`,
		// enum cookie: cookie guard, non-empty guard, cast
		`if c, err := r.Cookie("theme"); err == nil {`,
		`if _v := c.Value; _v != ""`,
		`_w := types.Color(_v)`,
		`req.Theme = &_w`,
		// plain string cookie: cookie guard, non-empty guard, address of _v
		`if c, err := r.Cookie("sid"); err == nil {`,
		`req.Sid = &_v`,
	)
}

// An optional enum or scalar query field points at a cast copy of the raw string.
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
	if err := generateTransport(pkg, sampleConfig(), root, nil); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, "internal/transport/s/search.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	mustParseGo(t, got)
	mustContainAll(t, got,
		`_w := types.Color(_v)`,
		`req.Sort = &_w`,
		`_w := types.Email(_v)`,
		`req.Cc = &_w`,
		// A plain *string takes the address of the raw value.
		`req.Plain = &_v`,
	)
}

// An enum @default pre-fills the member's Go constant.
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
	if err := generateTransport(pkg, sampleConfig(), root, nil); err != nil {
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
	if err := generateTransport(pkg, sampleConfig(), root, nil); err != nil {
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
	// Headers are set before the encode, whose first write sends them.
	if idx := strings.Index(body, "server.JSON().Encode"); idx >= 0 {
		pre := body[:idx]
		if !strings.Contains(pre, "w.Header().Set(\"etag\"") {
			t.Error("expected response header write to precede body encode")
		}
	}

	typesOut, err := os.ReadFile(filepath.Join(root, "internal/types", "design", "types.go"))
	if err == nil {
		// types.go is generated separately; only assert when present.
		typesSrc := string(typesOut)
		if strings.Contains(typesSrc, `Etag string `+"`json:\"etag\"`") {
			t.Errorf("etag field should be tagged json:\"-\":\n%s", typesSrc)
		}
	}
}

// A response @header or @cookie name argument sets the wire name the handler writes.
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
	if err := generateTransport(pkg, sampleConfig(), root, nil); err != nil {
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
	for _, banned := range []string{
		`w.Header().Set("total"`,
		`Cookie{Name: "sessionID"`,
	} {
		if strings.Contains(body, banned) {
			t.Errorf("explicit arg was ignored - found %q in:\n%s", banned, body)
		}
	}
}

// Non-string response headers and cookies are converted to their wire string, an optional
// header is nil-guarded, and an array header adds one value per element.
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
	if err := generateTransport(pkg, sampleConfig(), root, nil); err != nil {
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
		// A numeric scalar converts to int64 for strconv; a string scalar converts with string().
		`w.Header().Set("X-Price", strconv.FormatInt(int64(resp.Price), 10))`,
		`w.Header().Set("X-SKU", string(resp.Sku))`,
		`if resp.NextPage != nil {`,
		`w.Header().Set("X-Next-Page", *resp.NextPage)`,
		`for _, _v := range resp.Labels {`,
		`w.Header().Add("X-Label", _v)`,
		`http.SetCookie(w, &http.Cookie{Name: "flag", Value: strconv.FormatBool(resp.Active)})`,
		`http.SetCookie(w, &http.Cookie{Name: "plain", Value: resp.Plain})`,
	)
}

// A trailing {name...} variable binds the field of its name.
func TestGenerateTransportRestVariable(t *testing.T) {
	pkg := analyze(t, `package design
type FileReq { rest string }
type Resp { ok bool }
@prefix("/files/{rest...}")
service S { get Get / { request FileReq  response Resp } }`)
	root := t.TempDir()
	if err := generateTransport(pkg, sampleConfig(), root, nil); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(root, "internal/transport/s/get.go"))
	if err != nil {
		t.Fatal(err)
	}
	mustParseGo(t, string(out))
	mustContainAll(t, string(out), `req.Rest = r.PathValue("rest")`)
}

// A generic response writes its type-parameter header as the instance's
// argument: an int through strconv, an enum through string().
func TestGenerateTransportGenericResponseHeader(t *testing.T) {
	pkg := analyze(t, `package design
enum Prio { Low  High }
type Paged<T> { count T @header("X-Count")  items T[] }
service S {
    get Ints /ints { response Paged<int> }
    get Prios /prios { response Paged<Prio> }
}`)
	root := t.TempDir()
	if err := generateTransport(pkg, sampleConfig(), root, nil); err != nil {
		t.Fatal(err)
	}
	for file, want := range map[string]string{
		"ints.go":  `w.Header().Set("X-Count", strconv.Itoa(resp.Count))`,
		"prios.go": `w.Header().Set("X-Count", string(resp.Count))`,
	} {
		out, err := os.ReadFile(filepath.Join(root, "internal/transport/s", file))
		if err != nil {
			t.Fatal(err)
		}
		mustParseGo(t, string(out))
		mustContainAll(t, string(out), want)
	}
}

// A non-body field is tagged json:"-" while a body field keeps its tag.
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
	if err := generateTypes(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "types.go"))
	src := string(out)
	mustParseGo(t, src)
	for _, ident := range []string{"ID", "Q", "Auth", "Sess"} {
		if !lineHas(src, ident, `json:"-"`) {
			t.Errorf("expected %q with json:\"-\" tag:\n%s", ident, src)
		}
	}
	if !lineHas(src, "Payload", `json:"payload"`) {
		t.Errorf("expected payload field to keep its JSON tag:\n%s", src)
	}
}

// lineHas reports whether a line of src holds both ident and tag.
func lineHas(src, ident, tag string) bool {
	for _, line := range strings.Split(src, "\n") {
		if strings.Contains(line, ident) && strings.Contains(line, tag) {
			return true
		}
	}
	return false
}

// ---------- routes ----------

// The UserService routes file matches its golden.
func TestGenerateRoutesPatterns(t *testing.T) {
	pkg := analyze(t, handlerSampleDSL)
	root := t.TempDir()
	cfg := sampleConfig()
	if err := genRoutes(t, pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(root, "internal/routes/user-service/routes.go"))
	if err != nil {
		t.Fatal(err)
	}
	mustParseGo(t, string(out))
	expectGolden(t, "routes-user-service.go", string(out))
}

// A route lists service middlewares before the method's, in source order; the first is outermost.
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
	if err := genRoutes(t, pkg, cfg, root); err != nil {
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

// Services sharing a @group share one routes.go and one umbrella call.
func TestGenerateRoutesMergesServicesSharingGroup(t *testing.T) {
	pkg := analyze(t, `package design

type Thing { id string }

@group("shared/v1")
service Alpha {
    get A /a { response Thing }
}

@group("shared/v1")
service Beta {
    get B /b { response Thing }
}`)
	root := t.TempDir()
	if err := genRoutes(t, pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(root, "internal/routes/shared/v1/routes.go"))
	if err != nil {
		t.Fatalf("shared group must emit one routes.go: %v", err)
	}
	src := string(out)
	mustParseGo(t, src)
	for _, want := range []string{
		`srv.Handle("GET /v1/a", transportSharedV1.A(svcCtx))`,
		`srv.Handle("GET /v1/b", transportSharedV1.B(svcCtx))`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("merged routes file missing %q in:\n%s", want, src)
		}
	}
	if n := strings.Count(src, "srv.Handle("); n != 2 {
		t.Errorf("want both services' routes in one file, got %d", n)
	}
	if !strings.Contains(src, "wires every Alpha and Beta endpoint") {
		t.Errorf("doc comment should name every contributor:\n%s", src)
	}
	// The umbrella registers per directory: ServeMux panics on a pattern registered twice.
	all, err := os.ReadFile(filepath.Join(root, "internal/routes/routes.go"))
	if err != nil {
		t.Fatal(err)
	}
	mustParseGo(t, string(all))
	if n := strings.Count(string(all), ".RegisterRoutes(srv, svcCtx)"); n != 1 {
		t.Errorf("want 1 umbrella call for the shared directory, got %d:\n%s", n, all)
	}
}

// Services merged into one routes.go keep their own middleware chains, and a middleware an
// extend repeats is still wrapped once.
func TestGenerateRoutesMergedGroupKeepsPerServiceMiddleware(t *testing.T) {
	pkg := analyze(t, `package design

type Thing { id string }

middleware Auth
middleware RateLimit

@middlewares(Auth)
@group("shared/v1")
service Alpha {
    get A /a { response Thing }
}

@middlewares(Auth, RateLimit)
@group("shared/v1")
extend service Alpha {
    get AExt /a-ext { response Thing }
}

@group("shared/v1")
service Beta {
    get B /b { response Thing }
}

extend service Beta {
    get BExt /b-ext { response Thing }
}`)
	root := t.TempDir()
	if err := genRoutes(t, pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(root, "internal/routes/shared/v1/routes.go"))
	if err != nil {
		t.Fatalf("shared group must emit one routes.go: %v", err)
	}
	src := string(out)
	mustParseGo(t, src)
	for _, want := range []string{
		// Alpha: inherited chain, and the extend's repeat of Auth folded in.
		`srv.Handle("GET /v1/a", transportSharedV1.A(svcCtx), svcCtx.Auth)`,
		`srv.Handle("GET /v1/a-ext", transportSharedV1.AExt(svcCtx), svcCtx.Auth, svcCtx.RateLimit)`,
		// Beta: no middleware of its own, and none borrowed from Alpha.
		`srv.Handle("GET /v1/b", transportSharedV1.B(svcCtx))`,
		`srv.Handle("GET /v1/b-ext", transportSharedV1.BExt(svcCtx))`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("merged routes file missing %q in:\n%s", want, src)
		}
	}
	if n := strings.Count(src, "srv.Handle("); n != 4 {
		t.Errorf("want 4 routes from both services and their extends, got %d", n)
	}
	if strings.Contains(src, "svcCtx.Auth, svcCtx.Auth") {
		t.Errorf("extend repeating the inherited middleware must fold:\n%s", src)
	}
	all, err := os.ReadFile(filepath.Join(root, "internal/routes/routes.go"))
	if err != nil {
		t.Fatal(err)
	}
	mustParseGo(t, string(all))
	if n := strings.Count(string(all), ".RegisterRoutes(srv, svcCtx)"); n != 1 {
		t.Errorf("want 1 umbrella call for the shared directory, got %d:\n%s", n, all)
	}
}

// A middleware named at two inheritance layers is wrapped once, at its first (outermost) position.
func TestGenerateRoutesDedupsRepeatedMiddleware(t *testing.T) {
	pkg := analyze(t, `package design

type Thing { id string }

middleware Auth
middleware RateLimit

@middlewares(Auth)
service S {
    get A /a { response Thing }
}

@middlewares(Auth, RateLimit)
extend service S {
    get B /b { response Thing }

    @middlewares(Auth)
    get C /c { response Thing }
}`)
	root := t.TempDir()
	if err := genRoutes(t, pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(root, "internal/routes/s/routes.go"))
	src := string(out)
	mustParseGo(t, src)
	for _, want := range []string{
		`srv.Handle("GET /v1/a", transport.A(svcCtx), svcCtx.Auth)`,
		`srv.Handle("GET /v1/b", transport.B(svcCtx), svcCtx.Auth, svcCtx.RateLimit)`,
		`srv.Handle("GET /v1/c", transport.C(svcCtx), svcCtx.Auth, svcCtx.RateLimit)`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("expected deduped chain %q in:\n%s", want, src)
		}
	}
	if strings.Contains(src, "svcCtx.Auth, svcCtx.Auth") {
		t.Errorf("middleware repeated across layers must be wrapped once:\n%s", src)
	}
}

// @ignoreMiddleware drops the inherited chain, leaving only the method's own middlewares.
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
	if err := genRoutes(t, pkg, cfg, root); err != nil {
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

// @group replaces the service directory for transport and routes and stays out of the pattern.
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
	if err := genRoutes(t, pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	if err := generateTransport(pkg, cfg, root, nil); err != nil {
		t.Fatal(err)
	}

	// The routes file lives in the group folder and imports the group's transport.
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

	// Handlers land in the group folder.
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

// An extend block with its own @group gets its own transport package and routes file.
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
	if err := genRoutes(t, pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	if err := generateTransport(pkg, cfg, root, nil); err != nil {
		t.Fatal(err)
	}

	// The primary method stays in the service folder; the extend's lands in its group.
	for _, rel := range []string{
		"internal/transport/catalog/list-things.go",
		"internal/transport/v2/list-things-v2.go",
	} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("expected generated file %s: %v", rel, err)
		}
	}

	// The grouped handler renders errors through server.WriteError, with no per-package helper.
	grouped, _ := os.ReadFile(filepath.Join(root, "internal/transport/v2/list-things-v2.go"))
	if strings.Contains(string(grouped), "roottransport") {
		t.Error("the grouped handler should not import a root transport package")
	}
	mustContainAll(t, string(grouped), "server.WriteError(w, r, err)")
	if _, err := os.Stat(filepath.Join(root, "internal/transport/v2/errors.go")); err == nil {
		t.Error("no per-package errors.go should be emitted; errors render via server.WriteError")
	}

	// Each routes file imports and registers only its own group's transport.
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

// An extend without @group inherits the primary's; one with its own @group overrides it.
func TestGenerateExtendInheritsPrimaryGroup(t *testing.T) {
	pkg := analyze(t, `package design

type Thing { id string }

@prefix("/v1")
@group("admin")
service Catalog {
    get ListThings /things { response Thing }
}

extend service Catalog {
    get Inherited /more { response Thing }
}

@group("legacy")
extend service Catalog {
    get Old /old { response Thing }
}`)
	root := t.TempDir()
	cfg := sampleConfig()
	if err := genRoutes(t, pkg, cfg, root); err != nil {
		t.Fatal(err)
	}
	if err := generateTransport(pkg, cfg, root, nil); err != nil {
		t.Fatal(err)
	}

	for _, rel := range []string{
		"internal/transport/admin/list-things.go",
		"internal/transport/admin/inherited.go", // inherited the primary @group
		"internal/transport/legacy/old.go",      // own @group overrides
	} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("expected generated file %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "internal/transport/catalog/inherited.go")); err == nil {
		t.Error("ungrouped extend must inherit the primary @group, not land at the service root")
	}

	// The primary group's routes file registers the inherited method too.
	adminRoutes, _ := os.ReadFile(filepath.Join(root, "internal/routes/admin/routes.go"))
	rsrc := string(adminRoutes)
	mustParseGo(t, rsrc)
	mustContainAll(t, rsrc, "transportAdmin.ListThings(svcCtx)", "transportAdmin.Inherited(svcCtx)")
}

// @timeout and @maxBodySize wrap the route in server.WithLimits.
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
	if err := genRoutes(t, pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(root, "internal/routes/s/routes.go"))
	body := string(out)
	mustParseGo(t, body)
	mustContainAll(t, body,
		`"time"`,
		"server.WithLimits",
		"Timeout: 500 * time.Millisecond",
		"MaxBodySize: 1 << 10",
	)
}

// @timeout and @maxBodySize apply to a @passthrough route like any other.
func TestGenerateRoutesPassthroughAppliesTimeout(t *testing.T) {
	src := `package design
service S {
    @passthrough
    @timeout(5s)
    @maxBodySize(2048)
    get Live /live {}
}`
	pkg := analyze(t, src)
	root := t.TempDir()
	if err := genRoutes(t, pkg, sampleConfig(), root); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(root, "internal/routes/s/routes.go"))
	mustParseGo(t, string(body))
	mustContainAll(t, string(body),
		`"time"`,
		"Timeout: 5 * time.Second",
		"MaxBodySize: 2 << 10",
	)
}

// A size is a shift of the largest unit that divides it, else a byte count.
func TestFormatSizeGo(t *testing.T) {
	for _, tc := range []struct {
		n    int64
		want string
	}{
		{1 << 30, "1 << 30"},
		{3 << 30, "3 << 30"},
		{1536 << 20, "1536 << 20"},
		{32 << 20, "32 << 20"},
		{12 << 20, "12 << 20"},
		{1 << 10, "1 << 10"},
		{1536, "1536"},
		{1000, "1000"},
		{1, "1"},
	} {
		if got := formatSizeGo(tc.n); got != tc.want {
			t.Errorf("formatSizeGo(%d) = %q, want %q", tc.n, got, tc.want)
		}
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
	if err := genRoutes(t, pkg, cfg, root); err != nil {
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
	if err := generateService(pkg, cfg, root, nil); err != nil {
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
	pingSrc, _ := os.ReadFile(filepath.Join(dir, "ping.go"))
	if !strings.Contains(string(pingSrc), "func (l *PingService) Ping() error {") {
		t.Errorf("Ping logic signature mismatch:\n%s", pingSrc)
	}
	getSrc, _ := os.ReadFile(filepath.Join(dir, "get-user.go"))
	if !strings.Contains(string(getSrc), "func (l *GetUserService) GetUser(req *types.GetUserReq) (*types.User, error)") {
		t.Errorf("GetUser logic signature mismatch:\n%s", getSrc)
	}
}

// A service stub renders generic request and response types with their type arguments.
func TestGenerateServiceGenericInstantiation(t *testing.T) {
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
type GridReq { v string }
service S {
    post Create /c   { request CreateReq  response Page<User> }
    post Echo   /e   { request EchoReq    response Envelope<Email> }
    post Wrap   /w   { request WrapReq    response Page<Envelope<User>> }
    post Pair   /p   { request PairReq    response Pair<User, Email> }
    post Mix    /m   { request Page<User> response Envelope<User> }
    post Grid   /g   { request GridReq    response Page<map<string, User>[]> }
}`
	pkg := analyze(t, src)
	root := t.TempDir()
	if err := generateService(pkg, sampleConfig(), root, nil); err != nil {
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
		// Generic on the request side too.
		{"mix.go", "(req *types.Page[types.User])"},
		{"mix.go", "(*types.Envelope[types.User], error)"},
		// An array-of-maps argument keeps its `[]`.
		{"grid.go", "(*types.Page[[]map[string]types.User], error)"},
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
	if err := generateService(pkg, cfg, root, nil); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(existing)
	if string(out) != string(custom) {
		t.Errorf("scaffold overwrote user file:\n%s", out)
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
	if !wire.IsBodyVerb("post") || !wire.IsBodyVerb("PUT") || !wire.IsBodyVerb("PATCH") {
		t.Error("expected body verbs to be true")
	}
	if wire.IsBodyVerb("GET") || wire.IsBodyVerb("DELETE") {
		t.Error("expected non-body verbs to be false")
	}
	// A malformed @prefix contributes nothing to the route.
	svc := &ast.ServiceDecl{
		Decorators: []*ast.Decorator{
			{Name: "tags", Args: []*ast.DecoratorArg{{Value: &ast.StringLit{Value: "x"}}}},
			{Name: "prefix"}, // no args → ignored
			{Name: "prefix", Args: []*ast.DecoratorArg{{Value: &ast.IntLit{Value: 1}}}}, // wrong type → ignored
		},
	}
	m := &ast.Method{Name: "Ping"}
	if got := route.Resolve("", svc, m); got != "/ping" {
		t.Errorf("malformed @prefix must be ignored; got %q", got)
	}
	if got := route.Resolve("", nil, m); got != "/ping" {
		t.Errorf("nil service must yield bare method route; got %q", got)
	}
}

// ---------- end-to-end ----------

func TestGeneratePipelineEndToEnd(t *testing.T) {
	pkg := analyze(t, handlerSampleDSL)
	root := t.TempDir()
	cfg := sampleConfig()
	for _, step := range []func() error{
		func() error { return generateTransport(pkg, cfg, root, nil) },
		func() error { return genRoutes(t, pkg, cfg, root) },
		func() error { return generateService(pkg, cfg, root, nil) },
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
	if err := generateTransport(pkg, cfg, root, nil); err != nil {
		t.Fatal(err)
	}
	if err := generateService(pkg, cfg, root, nil); err != nil {
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
	if err := generateTransport(pkg, cfg, root, nil); err != nil {
		t.Fatal(err)
	}
	handler, _ := os.ReadFile(filepath.Join(root, "internal/transport/upload-service/upload.go"))
	mustParseGo(t, string(handler))
	mustContainAll(t, string(handler),
		"r.ParseMultipartForm(32 << 20)",
		// Handler-scoped cleanup frees temp files before the flush and on panic paths.
		"defer func() { _ = r.MultipartForm.RemoveAll() }()",
		`r.FormValue("note")`,
		`r.FormFile("avatar")`,
		"req.Avatar = header",
	)
	if strings.Contains(string(handler), "server.JSON().Decode(r.Body") {
		t.Errorf("multipart handler must not JSON-decode body:\n%s", handler)
	}
}

// A body the multipart parser refuses is a failed validation, which
// server.WriteValidationError answers: 413 past the body cap, else 400.
func TestGenerateTransportMultipartParseFailureIsValidationError(t *testing.T) {
	pkg := analyze(t, multipartSampleDSL)
	root := t.TempDir()
	if err := generateTransport(pkg, sampleConfig(), root, nil); err != nil {
		t.Fatal(err)
	}
	handler, err := os.ReadFile(filepath.Join(root, "internal/transport/upload-service/upload.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(handler)
	mustParseGo(t, src)
	_, afterParse, parses := strings.Cut(src, "r.ParseMultipartForm(")
	failure, _, _ := strings.Cut(afterParse, "return")
	if !parses || !strings.Contains(failure, "server.WriteValidationError(w, r, err)") {
		t.Errorf("a parse failure must go to server.WriteValidationError:\n%s", src)
	}
	mustContainNone(t, src, "http.Error(")
}

// The multipart memory budget is 32 MiB, or @maxBodySize when larger, written
// in the largest size unit that divides it.
func TestGenerateTransportMultipartBudget(t *testing.T) {
	for _, tc := range []struct{ decorator, want string }{
		{"", "r.ParseMultipartForm(32 << 20)"},
		{"@maxBodySize(1MB)", "r.ParseMultipartForm(32 << 20)"},
		{"@maxBodySize(64MB)", "r.ParseMultipartForm(64 << 20)"},
		{"@maxBodySize(1.5GB)", "r.ParseMultipartForm(1536 << 20)"},
	} {
		pkg := analyze(t, `package design
type UploadReq { avatar file }
service S {
    `+tc.decorator+`
    post Upload /upload { request UploadReq }
}`)
		root := t.TempDir()
		if err := generateTransport(pkg, sampleConfig(), root, nil); err != nil {
			t.Fatal(err)
		}
		handler, err := os.ReadFile(filepath.Join(root, "internal/transport/s/upload.go"))
		if err != nil {
			t.Fatal(err)
		}
		mustParseGo(t, string(handler))
		if !strings.Contains(string(handler), tc.want) {
			t.Errorf("%q: want %s:\n%s", tc.decorator, tc.want, handler)
		}
	}
}

// An explicit @form name is the key r.FormValue and r.FormFile read.
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
	if err := generateTransport(pkg, sampleConfig(), root, nil); err != nil {
		t.Fatal(err)
	}
	handler, _ := os.ReadFile(filepath.Join(root, "internal/transport/upload-service/upload.go"))
	mustParseGo(t, string(handler))
	mustContainAll(t, string(handler),
		`r.FormValue("note_text")`,
		`r.FormFile("avatar_file")`,
	)
	if strings.Contains(string(handler), `r.FormValue("caption")`) || strings.Contains(string(handler), `r.FormFile("pic")`) {
		t.Errorf("explicit @form name ignored - form key fell back to the field name:\n%s", handler)
	}
}

// semantic.FlattenFields collects a field from a mixin nested inside a cross-package mixin.
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
	r := buildProjectResolver(proj, newFixtureConfig(), "app")
	got := map[string]bool{}
	var names []string
	for _, ff := range semantic.FlattenFields(appPkg.Types["Req"], "", r.Resolver, nil) {
		f := ff.Field
		got[f.Name] = true
		names = append(names, f.Name)
	}
	// own = direct, mid = one cross-pkg level, deep = two levels (nested).
	for _, want := range []string{"own", "mid", "deep"} {
		if !got[want] {
			t.Errorf("semantic.RequestFieldList dropped %q (nested cross-pkg mixin field); collected %v", want, names)
		}
	}
}

// A qualified request type's bare nested mixin resolves in the request's package and binds.
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
	r := buildProjectResolver(proj, newFixtureConfig(), "app")
	m := &ast.Method{
		Verb:    "post",
		Request: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"shared", "Holder"}}},
		Path:    &ast.Path{Segments: []*ast.PathSegment{{Literal: "h"}, {Param: true, Literal: "id"}}},
	}
	got := map[string]wire.Binding{}
	var names []string
	for _, rf := range resolveRequestFields(m, appPkg, r) {
		got[rf.Field.Name] = rf.Binding
		names = append(names, rf.Field.Name)
	}
	if got["q"] != wire.BindQuery {
		t.Errorf("q should bind @query (from the cross-pkg request's bare mixin); got %v, fields %v", got["q"], names)
	}
	if _, ok := got["bod"]; !ok {
		t.Errorf("bod (cross-pkg request body field) dropped; fields %v", names)
	}
	if got["id"] != wire.BindPath {
		t.Errorf("id should bind @path; got %v, fields %v", got["id"], names)
	}
}

// A file[] field binds from MultipartForm.File and a file? field from r.FormFile.
func TestGenerateTransportMultipartFileArray(t *testing.T) {
	pkg := analyze(t, `package design
type BatchReq {
	files file[]
	cover file?
	note  string
}
type Resp { ok bool }
@prefix("/media")
service MediaService {
	post BatchUpload /batch {
		request  BatchReq
		response Resp
	}
}`)
	root := t.TempDir()
	if err := generateTransport(pkg, sampleConfig(), root, nil); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(root, "internal/transport/media-service/batch-upload.go"))
	if err != nil {
		t.Fatalf("missing handler: %v", err)
	}
	src := string(out)
	mustParseGo(t, src)
	mustContainAll(t, src, `req.Files = r.MultipartForm.File["files"]`)
	mustContainAll(t, src, `r.FormFile("cover")`, `req.Cover = header`)
	if strings.Contains(src, `r.FormFile("files")`) {
		t.Errorf("file[] must bind from MultipartForm.File, not r.FormFile:\n%s", src)
	}
}

// genRoutes writes pkg's routes files and the umbrella as a single-package project.
func genRoutes(t *testing.T, pkg *semantic.Package, cfg *config.Config, root string) error {
	t.Helper()
	if err := generateRoutes(pkg, cfg, root); err != nil {
		return err
	}
	proj := &semantic.Project{Packages: map[string]*semantic.Package{pkg.Name: pkg}}
	return generateProjectRoutesUmbrella(proj, cfg, root)
}

// The routes file imports time only for a duration, not for a group name ending in time.
func TestRoutesImportTimeFollowsTheDuration(t *testing.T) {
	cases := []struct {
		name      string
		decorator string
		want      bool
	}{
		{"group ending in time, no timeout", "", false},
		{"group ending in time, with timeout", "@timeout(30s)", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pkg := analyze(t, `package p
type Thing { id string }
@group("uptime")
service S {
    `+c.decorator+`
    get Ping /ping {
        response Thing
    }
}`)
			root := t.TempDir()
			if err := genRoutes(t, pkg, sampleConfig(), root); err != nil {
				t.Fatal(err)
			}
			out, err := os.ReadFile(filepath.Join(root, "internal/routes/uptime/routes.go"))
			if err != nil {
				t.Fatal(err)
			}
			src := string(out)
			// mustParseGo also fails on an unused time import.
			mustParseGo(t, src)
			if got := strings.Contains(src, `"time"`); got != c.want {
				t.Errorf("imports time = %v, want %v:\n%s", got, c.want, src)
			}
		})
	}
}
