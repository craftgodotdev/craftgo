package golang

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	if !strings.Contains(src, "registers the Alpha and Beta routes of group shared/v1 on srv") {
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
