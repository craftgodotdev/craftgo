package golang

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The wiring package is what main.go calls, and main.go is generated once.
// So Register has to exist with the same signature for every design - with
// routes, with consumers, with both, with neither - or a design that later
// gains or loses either leaves a frozen main.go calling something that is
// no longer there.
func TestWiringRegisterSurfaceIsTheSameForEveryDesign(t *testing.T) {
	const httpSrc = `package web
type Thing { id string }
type GetReq { id string @path }
service WebService {
	get GetThing /things/{id} { request GetReq  response Thing }
}`
	const emptySrc = `package empty
type Unused { id string }`

	cases := []struct {
		name    string
		sources []string
		body    []string
		absent  []string
	}{
		{
			name:    "routes only",
			sources: []string{httpSrc},
			body:    []string{"routes.RegisterAll(srv, svcCtx)", "return func(context.Context) error { return nil }, nil"},
		},
		{
			name:    "events only",
			sources: []string{ordersSrc, notifySrc},
			body:    []string{"return func(context.Context) error { return nil }, nil"},
			absent:  []string{"routes.RegisterAll", "internal/routes", "internal/events", "Bus"},
		},
		{
			name:    "both",
			sources: []string{httpSrc, ordersSrc, notifySrc},
			body:    []string{"routes.RegisterAll(srv, svcCtx)"},
			absent:  []string{"internal/events", "Bus"},
		},
		{
			name:    "neither",
			sources: []string{emptySrc},
			body:    []string{"return func(context.Context) error { return nil }, nil"},
			absent:  []string{"routes.RegisterAll", "internal/routes", "internal/transport"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			proj := analyzeProject(t, c.sources...)
			cfg := eventsConfig()
			dir := t.TempDir()
			if err := generateWiring(proj, cfg, dir); err != nil {
				t.Fatalf("generate wiring: %v", err)
			}
			got := readGen(t, dir, "internal/wiring/wiring.go")
			mustParseGo(t, got)
			// The signature is the fixed part; only the body varies.
			mustContainAll(t, got,
				"func Register(ctx context.Context, srv *server.Server, svcCtx *svccontext.ServiceContext) (func(context.Context) error, error) {",
			)
			mustContainAll(t, got, c.body...)
			if len(c.absent) > 0 {
				mustContainNone(t, got, c.absent...)
			}
		})
	}
}

// Register hands back a shutdown for every design, consumers or not. The
// handle is what lets delivery be drained beside srv.Stop; adding it later
// would mean changing a signature that main.go - generated once - already
// calls, which is the migration this whole indirection exists to avoid.
func TestWiringAlwaysReturnsAShutdown(t *testing.T) {
	const httpSrc = `package web
type Thing { id string }
type GetReq { id string @path }
service WebService {
	get GetThing /things/{id} { request GetReq  response Thing }
}`
	for _, c := range []struct {
		name    string
		sources []string
	}{
		{"routes only", []string{httpSrc}},
		{"events only", []string{ordersSrc, notifySrc}},
	} {
		t.Run(c.name, func(t *testing.T) {
			proj := analyzeProject(t, c.sources...)
			dir := t.TempDir()
			if err := generateWiring(proj, eventsConfig(), dir); err != nil {
				t.Fatalf("generate wiring: %v", err)
			}
			got := readGen(t, dir, "internal/wiring/wiring.go")
			mustParseGo(t, got)
			mustContainAll(t, got, "(func(context.Context) error, error) {")
		})
	}
}

// The routes umbrella is removed when the design stops declaring routes.
// Skipping the write instead leaves a generated file calling into
// per-service packages this run no longer emits - and because the emitter
// returns before it can rewrite anything, `craftgo gen` cannot repair it:
// the user has to delete the file by hand.
func TestRoutesUmbrellaGoesWhenTheLastRouteDoes(t *testing.T) {
	dir := t.TempDir()
	cfg := eventsConfig()
	routesDir := filepath.Join(dir, cfg.Output.Routes)
	if err := os.MkdirAll(routesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(routesDir, "routes.go")
	if err := os.WriteFile(stale, []byte(generatedHeader+"\n\npackage routes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An events-only design: every service declares contracts, no method.
	proj := analyzeProject(t, ordersSrc, notifySrc)
	if err := generateProjectRoutesUmbrella(proj, cfg, dir); err != nil {
		t.Fatalf("routes umbrella: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		body, _ := os.ReadFile(stale)
		t.Errorf("routes.go survived a design with no route (%v):\n%s", err, body)
	}
}

// main.go is written once with the wiring import baked in, and nothing
// prunes a directory `output.wiring` used to name. Moving the key leaves
// main.go compiling against the copy at the old path, whose body stops
// tracking the design - silently, because both copies carry the same
// exported surface.
func TestWiringNoteWhenMainImportsAnotherWiringPackage(t *testing.T) {
	cases := []struct {
		name   string
		wiring string
		want   bool
	}{
		{"key unchanged", "./internal/wiring", false},
		{"key moved", "./internal/wire", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			proj := analyzeProject(t, ordersSrc, notifySrc)
			cfg := eventsConfig()
			cfg.Output.Main = "./main.go"
			cfg.Output.Wiring = c.wiring
			dir := t.TempDir()
			main := "package main\n\nimport \"example.com/app/internal/wiring\"\n\n" +
				"func main() {\n\tsvc.Events = svccontext.NewEvents(bus)\n\t_, _ = wiring.Register(ctx, srv, svc)\n}\n"
			if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(main), 0o644); err != nil {
				t.Fatal(err)
			}
			var got bool
			for _, note := range EventOutputNotes(proj, cfg, dir) {
				if strings.Contains(note, "imports a wiring package other than") {
					got = true
				}
			}
			if got != c.want {
				t.Errorf("stale-wiring note = %v, want %v (notes: %v)", got, c.want, EventOutputNotes(proj, cfg, dir))
			}
		})
	}
}
