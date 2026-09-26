package golang

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
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
	encode := strings.Index(body, "server.JSON().Encode")
	if encode < 0 || !strings.Contains(body[:encode], `w.Header().Set("etag"`) {
		t.Errorf("the etag header must be set before the body is encoded:\n%s", body)
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
	norm := collapseSpace(src)
	for _, want := range []string{
		"ID string `json:\"-\" path:\"id\"`",
		"Q string `json:\"-\" query:\"q\"`",
		"Auth string `json:\"-\" header:\"auth\"`",
		"Sess string `json:\"-\" cookie:\"sess\"`",
		"Payload string `json:\"payload\"`",
	} {
		if !strings.Contains(norm, want) {
			t.Errorf("missing %s in:\n%s", want, src)
		}
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
func TestFlattenFieldsNestedCrossPkgMixin(t *testing.T) {
	proj := analyzeFiles(t, map[string]string{
		"shared/types.craftgo": `package shared
type Inner { deep int32? @default(7) }
type Outer { Inner  mid int64? @default(9) }`,
		"app/types.craftgo": `package app
import "shared"
type Req { shared.Outer  own string }`,
	})
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
			t.Errorf("semantic.FlattenFields dropped %q (nested cross-pkg mixin field); collected %v", want, names)
		}
	}
}

// A qualified request type's bare nested mixin resolves in the request's package and binds.
func TestResolveRequestFieldsQualifiedRequestNestedMixin(t *testing.T) {
	proj := analyzeFiles(t, map[string]string{
		"shared/types.craftgo": `package shared
type Sub { q string @query @length(2, 5)  bod string @length(1, 10) }
type Holder { Sub  id string @path }`,
		"app/types.craftgo": `package app
import "shared"
type Resp { ok bool }
service S { post DoIt /h/{id} { request shared.Holder  response Resp } }`,
	})
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
