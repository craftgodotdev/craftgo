package semantic

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// ---------- pathless-method route engine ----------

// TestResolveMethodPathPathlessUsesIdentsKebab pins the analyzer's
// pathless-method auto-route to the canonical word-split codegen registers
// the route with (idents.SplitFieldName). The two engines diverged on
// digit-boundary names — `ListV2Items` resolved to `/list-v2-items` here but
// `/list-v2items` in codegen — so the editor showed (and route-collision
// detection keyed on) a route the server never served.
func TestResolveMethodPathPathlessUsesIdentsKebab(t *testing.T) {
	a := &analyzer{}
	svc := &ast.ServiceDecl{}
	cases := map[string]string{
		"ListV2Items":  "/list-v2items",
		"OAuth2Login":  "/o-auth2login",
		"Base64Encode": "/base64encode",
		"GetUser":      "/get-user",
		"ListTodos":    "/list-todos",
	}
	for name, want := range cases {
		got := a.resolveMethodPath(svc, &ast.Method{Name: name})
		if got != want {
			t.Errorf("resolveMethodPath pathless %q = %q, want %q (idents canonical)", name, got, want)
		}
	}
}

// ---------- basePath format ----------

func TestBasePathFormatOK(t *testing.T) {
	cases := []string{"", "/", "/v1", "/api/v1"}
	for _, bp := range cases {
		_, diags := AnalyzeWith(parseFiles(t, `service S {}`), Options{BasePath: bp})
		if findCode(diags, CodePathBaseFormat) != nil {
			t.Errorf("basePath %q should be OK, got %v", bp, codes(diags))
		}
	}
}

func TestBasePathFormatRejectsMissingSlash(t *testing.T) {
	_, diags := AnalyzeWith(parseFiles(t, `service S {}`), Options{BasePath: "v1"})
	d := findCode(diags, CodePathBaseFormat)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if d.Severity != lexer.SeverityWarning {
		t.Errorf("expected warning severity, got %v", d.Severity)
	}
}

func TestBasePathFormatRejectsTrailingSlash(t *testing.T) {
	_, diags := AnalyzeWith(parseFiles(t, `service S {}`), Options{BasePath: "/v1/"})
	if findCode(diags, CodePathBaseFormat) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestBasePathFormatRejectsDoubleSlash(t *testing.T) {
	_, diags := AnalyzeWith(parseFiles(t, `service S {}`), Options{BasePath: "/v1//api"})
	if findCode(diags, CodePathBaseFormat) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

// ---------- Cross-service collision ----------

func TestPathCollisionAcrossServices(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `@prefix("/v1")
service A { get GetUser /users {} }
@prefix("/v1")
service B { get List /users {} }`))
	d := findCode(diags, CodePathCollision)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "GET") || !strings.Contains(d.Msg, "/v1/users") {
		t.Errorf("msg = %q", d.Msg)
	}
	if len(d.Related) != 1 {
		t.Errorf("expected related to first declaration, got %+v", d.Related)
	}
}

func TestPathCollisionResolvedViaPrefix(t *testing.T) {
	// /v1/users (service A's @prefix) vs /v1/users (service B inline) - collision.
	_, diags := Analyze(parseFiles(t, `@prefix("/v1")
service A { get A /users {} }
service B { get B /v1/users {} }`))
	if findCode(diags, CodePathCollision) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestPathCollisionWithBasePath(t *testing.T) {
	// basePath stitches both services into the same final path.
	_, diags := AnalyzeWith(parseFiles(t,
		`service A { get A /users {} }
service B { get B /users {} }`),
		Options{BasePath: "/api"})
	if findCode(diags, CodePathCollision) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestPathDifferentVerbNoCollision(t *testing.T) {
	mustClean(t, `service A {
	get GetUser /users {}
	post CreateUser /users {}
}`)
}

func TestPathlessMethodsNoFalseCollision(t *testing.T) {
	// Two pathless methods of the same verb auto-route to distinct kebab
	// paths (`/ping`, `/health`), so they must NOT be flagged as a duplicate
	// route — the collision key uses the resolved route, not the empty path.
	mustClean(t, `service S {
	get Ping {}
	get Health {}
}`)
}

func TestSameServiceRouteCollisionStillFlagged(t *testing.T) {
	// Two methods with the same explicit path + verb in one service still
	// collide.
	_, diags := Analyze(parseFiles(t, `service S {
	get A /users {}
	get B /users {}
}`))
	if findCode(diags, CodeServiceDuplicateRoute) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestPathSameServiceDuplicateHandledByOtherCheck(t *testing.T) {
	// Same-service duplicate route is reported by checkServiceMethods,
	// NOT by path/collision (avoid double-fire).
	_, diags := Analyze(parseFiles(t, `service S {
	get A /users {}
	get B /users {}
}`))
	if findCode(diags, CodeServiceDuplicateRoute) == nil {
		t.Fatalf("expected service/duplicate-route, got %v", codes(diags))
	}
	if findCode(diags, CodePathCollision) != nil {
		t.Errorf("path/collision should not double-fire, got %v", codes(diags))
	}
}

// TestPathParamRenameStillCollides pins that two routes differing
// ONLY in the path-param name (`{id}` vs `{id1}`) are flagged as a
// collision (net/http registers both against the same pattern and
// panics at boot otherwise). Both same-service (caught by
// checkServiceMethods) and cross-service (caught by path/collision)
// paths must fire.
func TestPathParamRenameStillCollides(t *testing.T) {
	t.Run("same service", func(t *testing.T) {
		_, diags := Analyze(parseFiles(t, `type R {}
service S {
	get A /products/{id} { response R }
	get B /products/{id1} { response R }
}`))
		if findCode(diags, CodeServiceDuplicateRoute) == nil {
			t.Fatalf("rename-bypass must surface as duplicate-route; got %v", codes(diags))
		}
	})
	t.Run("cross service", func(t *testing.T) {
		_, diags := Analyze(parseFiles(t, `type R {}
service A { get GetX /items/{id} { response R } }
service B { get GetY /items/{itemId} { response R } }`))
		if findCode(diags, CodePathCollision) == nil {
			t.Fatalf("rename-bypass must surface across services; got %v", codes(diags))
		}
	})
	t.Run("nested params", func(t *testing.T) {
		// Multi-segment route with two params — different names in
		// BOTH slots must still collapse to the same shape.
		_, diags := Analyze(parseFiles(t, `type R {}
service A { get GetX /u/{u}/o/{o} { response R } }
service B { get GetY /u/{userId}/o/{orderId} { response R } }`))
		if findCode(diags, CodePathCollision) == nil {
			t.Fatalf("nested-param rename must collide; got %v", codes(diags))
		}
	})
	t.Run("literal vs param does NOT collide", func(t *testing.T) {
		// /products/abc and /products/{id} are different routes — net/http
		// dispatches literals before params — so the shape gate must
		// NOT false-positive here.
		mustClean(t, `type R {}
type Req { id string }
service A { get GetX /products/abc { response R } }
service B { get GetY /products/{id} { request Req  response R } }`)
	})
}

// ---------- Path param consistency ----------

func TestPathParamMatchesField(t *testing.T) {
	mustClean(t, `type Req { id string }
service S {
	get GetUser /users/{id} {
		request   Req
	}
}`)
}

func TestPathParamWithExplicitDecorator(t *testing.T) {
	mustClean(t, `type Req { rawId string @path("id") }
service S {
	get GetUser /users/{id} {
		request   Req
	}
}`)
}

func TestPathParamMissing(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type Req { name string }
service S {
	get GetUser /users/{id} {
		request   Req
	}
}`))
	d := findCode(diags, CodePathParamMissing)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "{id}") {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestPathParamOrphan(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type Req { id string @path("foo") }
service S {
	get GetUser /users/{id} {
		request   Req
	}
}`))
	d := findCode(diags, CodePathParamOrphan)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, `"foo"`) {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestPathParamWarnsWithoutRequest(t *testing.T) {
	// Path declares {id} but no request struct → the path value
	// has no Go-side binding. The analyser emits a warning so the
	// author either adds a request struct or accepts the path
	// param as informational (e.g. for a passthrough handler that
	// reaches `r.PathValue` directly).
	expectDiag(t, `service S {
	get GetUser /users/{id} {}
}`, CodePathParamMissing)
}

func TestPathParamPassthroughSkipsWarn(t *testing.T) {
	// Passthrough handlers reach `r.PathValue` directly through the
	// raw http.Request, so the warning would be spurious — suppress
	// it for the passthrough path.
	mustClean(t, `service S {
	@passthrough
	get Stream /users/{id}/feed {}
}`)
}

func TestPathParamSkippedForUnknownRequestType(t *testing.T) {
	// Unknown request type - placement check covers; we silently skip.
	_, diags := Analyze(parseFiles(t, `service S {
	get GetUser /users/{id} {
		request   Mystery
	}
}`))
	if findCode(diags, CodePathParamMissing) != nil {
		t.Errorf("unknown req type should not produce path/param-missing, got %v", codes(diags))
	}
}

// ---------- Health endpoint conflict ----------

func TestHealthConflict(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `service S {
	get HealthCheck /healthz {}
}`))
	d := findCode(diags, CodePathHealthConflict)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "/healthz") {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestHealthConflictReadyz(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `service S {
	get Ready /readyz {}
}`))
	if findCode(diags, CodePathHealthConflict) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestHealthConflictRespectsCustomList(t *testing.T) {
	// User overrides health paths to a non-conflicting set.
	files := parseFiles(t, `service S {
	get Health /healthz {}
}`)
	_, diags := AnalyzeWith(files, Options{HealthPaths: []string{"/_status"}})
	if findCode(diags, CodePathHealthConflict) != nil {
		t.Errorf("/healthz should not conflict when HealthPaths overrides it, got %v", codes(diags))
	}
}

func TestHealthConflictNonHealthPath(t *testing.T) {
	mustClean(t, `service S {
	get Status /status {}
}`)
}

// ---------- Helpers ----------

func TestExtractPathParams(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"/", nil},
		{"/users", nil},
		{"/users/{id}", []string{"id"}},
		{"/users/{id}/posts/{post}", []string{"id", "post"}},
		{"/{a}/{b}/{c}", []string{"a", "b", "c"}},
		{"/{unclosed", nil},
	}
	for _, c := range cases {
		got := extractPathParams(c.in)
		if !equalSlice(got, c.want) {
			t.Errorf("extractPathParams(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestResolveMethodPathFallbackName(t *testing.T) {
	// Method with no inline path: fallback is /<kebab(name)>.
	a := &analyzer{pkg: &Package{}}
	got := a.resolveMethodPath(nil, &ast.Method{Name: "Ping"})
	if got != "/ping" {
		t.Errorf("got %q, want %q", got, "/ping")
	}
}

func TestResolveMethodPathIgnoresGroup(t *testing.T) {
	// @group nests generated files on disk; it must NOT appear in the route.
	pkg, diags := AnalyzeWith(parseFiles(t, `@prefix("/v1")
@group("admin")
service S { get GetUser /users {} }`), Options{})
	if len(diags) > 0 {
		t.Fatalf("unexpected diags: %v", diags)
	}
	a := &analyzer{pkg: pkg, opts: Options{}}
	si := pkg.Services["S"]
	got := a.resolveMethodPath(si.Primary, si.Methods[0])
	if got != "/v1/users" {
		t.Errorf("got %q, want %q", got, "/v1/users")
	}
}

func TestResolveMethodPathEmptyParts(t *testing.T) {
	// No basePath, no prefix, no inline path → defaults to /<kebab>.
	a := &analyzer{pkg: &Package{}}
	got := a.resolveMethodPath(nil, &ast.Method{Name: "Ping"})
	if got != "/ping" {
		t.Errorf("got %q, want %q", got, "/ping")
	}
}

func TestPathBindingNameVariants(t *testing.T) {
	// Bare `@path` → field name; `@path("custom")` → custom; no
	// decorator → no binding.
	cases := []struct {
		f       *ast.Field
		want    string
		hasPath bool
	}{
		{&ast.Field{Name: "id"}, "", false},
		{&ast.Field{Name: "id", Decorators: []*ast.Decorator{{Name: "path"}}}, "id", true},
		{&ast.Field{Name: "id", Decorators: []*ast.Decorator{
			{Name: "path", Args: []*ast.DecoratorArg{{Value: &ast.StringLit{Value: "user-id"}}}},
		}}, "user-id", true},
		// Args present but not a string → fallback to field name.
		{&ast.Field{Name: "id", Decorators: []*ast.Decorator{
			{Name: "path", Args: []*ast.DecoratorArg{{Value: &ast.IntLit{}}}},
		}}, "id", true},
	}
	for i, c := range cases {
		got, has := pathBindingName(c.f)
		if got != c.want || has != c.hasPath {
			t.Errorf("case %d: got (%q, %v), want (%q, %v)", i, got, has, c.want, c.hasPath)
		}
	}
}

func TestRequestPathFieldsNilGuards(t *testing.T) {
	a := &analyzer{pkg: &Package{Types: map[string]*ast.TypeDecl{}}}
	env := a.pathParamEnv()
	// nil request
	if got := requestPathFields(&ast.Method{}, nil, env); got != nil {
		t.Error("nil request should return nil")
	}
	// qualified name unresolvable in this package → nil
	got := requestPathFields(&ast.Method{Request: &ast.NamedTypeRef{
		Name: &ast.QualifiedIdent{Parts: []string{"shared", "Req"}},
	}}, nil, env)
	if got != nil {
		t.Error("unresolved qualified ref should return nil")
	}
	// Request name is nil → skip
	got = requestPathFields(&ast.Method{Request: &ast.NamedTypeRef{Name: nil}}, nil, env)
	if got != nil {
		t.Error("nil Name should return nil")
	}
}

func TestRequestPathFieldsSkipsMixin(t *testing.T) {
	// Mixin in request body - request walker skips it (mixin pass owns
	// validation), exercising the `if !ok { continue }` branch.
	mustClean(t, `type Base { id string }
type Req { Base  name string }
service S {
	get GetUser /users/{id} {
		request   Req
	}
}`)
}

// TestResolveMethodPathBasePathMissingSlash exercises the line that
// repairs a path which doesn't start with `/`. Pairs naturally with
// the basePath format warning.
func TestResolveMethodPathBasePathMissingSlash(t *testing.T) {
	a := &analyzer{pkg: &Package{}, opts: Options{BasePath: "v1"}}
	got := a.resolveMethodPath(nil, &ast.Method{Name: "Ping"})
	// "v1" + "/ping" → "v1//ping" → "v1/ping" → "/v1/ping".
	if got != "/v1/ping" {
		t.Errorf("got %q, want %q", got, "/v1/ping")
	}
}

// TestWalkBodyForPathCyclicMixin covers the visited check inside
// walkBodyForPath. Real cyclic mixins are flagged by the mixin pass
// but path resolution still encounters the cycle and must not loop.
func TestWalkBodyForPathCyclicMixin(t *testing.T) {
	a := &analyzer{pkg: &Package{
		Types: map[string]*ast.TypeDecl{
			"A": {
				Name: "A",
				Body: []ast.TypeMember{
					&ast.Mixin{Ref: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"B"}}}},
				},
			},
			"B": {
				Name: "B",
				Body: []ast.TypeMember{
					&ast.Mixin{Ref: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"A"}}}},
					&ast.Field{Name: "id"},
				},
			},
		},
	}}
	out := &pathParamSet{all: map[string]bool{}}
	walkBodyForPath(a.pkg.Types["A"], "", "A", map[string]bool{"id": true}, out, map[string]bool{}, a.pathParamEnv())
	if !out.has("id") {
		t.Error("cyclic mixin should still surface reachable fields once")
	}
}

// TestWalkBodyForPathQualifiedNestedMixin covers the qualified-mixin
// skip inside walkBodyForPath (the recursive mixin walker shouldn't
// follow `shared.Foo` - qualified-ref pass handles it).
func TestWalkBodyForPathQualifiedNestedMixin(t *testing.T) {
	a := &analyzer{pkg: &Package{
		Types: map[string]*ast.TypeDecl{
			"A": {
				Name: "A",
				Body: []ast.TypeMember{
					&ast.Mixin{Ref: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"shared", "Foo"}}}},
					&ast.Field{Name: "id"},
				},
			},
		},
	}}
	out := &pathParamSet{all: map[string]bool{}}
	walkBodyForPath(a.pkg.Types["A"], "", "A", map[string]bool{"id": true}, out, map[string]bool{}, a.pathParamEnv())
	if !out.has("id") {
		t.Error("qualified mixin unresolvable in-package should be skipped, own fields still surface")
	}
}

// TestPathBindingNameSkipsNonPathDecorator covers the `if d.Name !=
// "path" { continue }` branch when a field has multiple decorators
// and only the last is @path.
func TestPathBindingNameSkipsNonPathDecorator(t *testing.T) {
	f := &ast.Field{Name: "id", Decorators: []*ast.Decorator{
		{Name: "doc", Args: []*ast.DecoratorArg{{Value: &ast.StringLit{Value: "x"}}}},
		{Name: "path"},
	}}
	got, has := pathBindingName(f)
	if !has || got != "id" {
		t.Errorf("got (%q, %v), want (id, true)", got, has)
	}
}

func TestCheckMethodPathParamsNilName(t *testing.T) {
	// Defensive: m.Request set but m.Request.Name nil - early-return
	// branch in checkMethodPathParams.
	a := &analyzer{pkg: &Package{Types: map[string]*ast.TypeDecl{}}}
	checkMethodPathParams("S", &ast.Method{
		Name:    "M",
		Pos:     lexer.Position{Line: 1},
		Request: &ast.NamedTypeRef{Name: nil},
	}, "/users", a.pathParamEnv())
	if len(a.diags) != 0 {
		t.Errorf("nil request name should not diag, got %v", a.diags)
	}
}

func TestPathSetHasNil(t *testing.T) {
	var s *pathParamSet
	if s.has("x") {
		t.Error("nil set should not have anything")
	}
}

// ---------- helper ----------

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
