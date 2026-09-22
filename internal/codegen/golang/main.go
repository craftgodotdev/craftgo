package golang

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/protodesign"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// mainData is the template input for `main.tmpl`. The one wiring call
// keeps the import set tiny - main.go references only `config`,
// `wiring`, `middleware`, `svccontext`, the runtime observability
// packages, and `pkg/rpc` when the design declares a proto service.
type mainData struct {
	ConfigImport string
	// WiringImport is the generated wiring package holding Register, the
	// one call main.go makes to attach the design. It is imported
	// unconditionally: Register exists for every project, so a design
	// that gains or loses routes leaves this file alone.
	WiringImport     string
	MiddlewareImport string
	SvccontextImport string
	Middlewares      []string
	HasMiddlewares   bool
	// HasDocs gates the in-process API-docs wiring: the `embed` import, the
	// embedded spec var, and the cfg.Docs ServeDocs call. False when the
	// OpenAPI document is disabled or lives outside the main package's tree
	// (go:embed cannot reach a `../` path).
	HasDocs bool
	// OpenAPIEmbed is the forward-slash path of the generated OpenAPI document
	// relative to main.go's directory, for the `//go:embed` directive.
	OpenAPIEmbed string
	// HasRoutes gates the HTTP listener: the server, its middleware, the
	// wiring.Register call, the docs and the drain.
	HasRoutes bool
	// HasGRPC gates the gRPC listener: the rpc server, its interceptors,
	// the wiring.RegisterGRPC call and the drain.
	HasGRPC bool
}

// generateProjectMain scaffolds the project's main.go (`output.main`)
// using the union of services and middlewares from every package. The
// single shared umbrella `routes.RegisterAll` (emitted by
// [generateProjectRoutesUmbrella]) is the one-call wire-up so the
// template doesn't have to import per-package routes packages.
//
// The file is gen-once: written when missing, skipped on subsequent
// gen runs so user-written boot code (extra middlewares, config
// loading, OTel SDK setup, etc.) survives regeneration.
//
// Setting `output.main: "-"` in the manifest opts the project out of
// scaffolding entirely - useful for test fixtures that ship their own
// httptest server and would collide with a generated `package main`.
func generateProjectMain(proj *semantic.Project, protos *protodesign.Set, cfg *config.Config, projectRoot string) error {
	if proj == nil {
		return nil
	}
	if cfg.Output.RuntimeDisabled() {
		return nil
	}
	// Skip when nothing is wireable: main.go boots the listeners, and a
	// design with no route and no RPC has none to boot. An events-only
	// deployable builds its own bus and calls the generated Register
	// itself.
	if !projectHasRoutes(proj) && !protos.HasServices() {
		return nil
	}
	dest := filepath.Join(projectRoot, cfg.Output.Main)
	if _, err := os.Stat(dest); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	data := buildProjectMainData(proj, protos, cfg)
	formatted, err := renderGo(tmpl("main.tmpl"), data)
	if err != nil {
		return fmt.Errorf("render main.go: %w", err)
	}
	return os.WriteFile(dest, formatted, 0o644)
}

// projectHasRoutes reports whether any service declares an HTTP method.
func projectHasRoutes(proj *semantic.Project) bool {
	for _, pkg := range proj.Packages {
		if pkg == nil {
			continue
		}
		for _, svc := range pkg.Services {
			if len(svc.Methods) > 0 {
				return true
			}
		}
	}
	return false
}

// buildProjectMainData unions every package's middleware names into
// one deterministic list. The umbrella RegisterAll already aggregates
// services so the template needs no further per-package wiring.
func buildProjectMainData(proj *semantic.Project, protos *protodesign.Set, cfg *config.Config) mainData {
	d := mainData{
		ConfigImport:     goImportFromRel(cfg.Package, cfg.Output.Config),
		WiringImport:     goImportFromRel(cfg.Package, cfg.Output.Wiring),
		MiddlewareImport: goImportFromRel(cfg.Package, cfg.Output.Middleware),
		SvccontextImport: goImportFromRel(cfg.Package, fileDirRel(cfg.Output.Svccontext)),
		HasRoutes:        projectHasRoutes(proj),
		HasGRPC:          protos.HasServices(),
	}
	seen := map[string]bool{}
	for _, k := range slices.Sorted(maps.Keys(proj.Packages)) {
		p := proj.Packages[k]
		if p == nil {
			continue
		}
		for _, name := range sortedMiddlewareNames(p) {
			if seen[name] {
				continue
			}
			seen[name] = true
			d.Middlewares = append(d.Middlewares, name)
		}
	}
	d.HasMiddlewares = len(d.Middlewares) > 0

	// Wire the in-process API docs only when there is an HTTP server to
	// serve them, the OpenAPI document is emitted, and it lives under
	// main.go's directory (go:embed cannot cross `..`).
	if spec := cfg.Output.OpenAPI; d.HasRoutes && spec != "" && spec != "-" {
		mainDir := filepath.Dir(filepath.Clean(cfg.Output.Main))
		if rel, err := filepath.Rel(mainDir, filepath.Clean(spec)); err == nil {
			rel = filepath.ToSlash(rel)
			if !strings.HasPrefix(rel, "../") {
				d.HasDocs = true
				d.OpenAPIEmbed = rel
			}
		}
	}
	return d
}

// operationNameFor extracts the last segment of a Go module path
// (`github.com/foo/myapp` → `myapp`) for use as the OTel span name.
// Falls back to a generic `api` when the input has no segments -
// keeps the generated main.go compilable even on degenerate
// configs.
func operationNameFor(modulePath string) string {
	for i := len(modulePath) - 1; i >= 0; i-- {
		if modulePath[i] == '/' {
			return modulePath[i+1:]
		}
	}
	if modulePath == "" {
		return "api"
	}
	return modulePath
}
