package golang

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Register keeps one signature for every design, since the gen-once main.go calls it.
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

// Register returns a shutdown func for every design, with or without events.
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

// An events-only design writes no routes umbrella.
func TestRoutesUmbrellaIsNotWrittenWithoutRoutes(t *testing.T) {
	dir := t.TempDir()
	cfg := eventsConfig()
	proj := analyzeProject(t, ordersSrc, notifySrc)
	if err := generateProjectRoutesUmbrella(proj, cfg, dir); err != nil {
		t.Fatalf("routes umbrella: %v", err)
	}
	stale := filepath.Join(dir, cfg.Output.Routes, "routes.go")
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("an events-only design must write no routes umbrella, stat returned %v", err)
	}
}

// A note fires when main.go imports a wiring package other than output.wiring.
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
			for _, note := range EventOutputNotes(proj, nil, cfg, dir) {
				if strings.Contains(note, "imports a wiring package other than") {
					got = true
				}
			}
			if got != c.want {
				t.Errorf("stale-wiring note = %v, want %v (notes: %v)", got, c.want, EventOutputNotes(proj, nil, cfg, dir))
			}
		})
	}
}
