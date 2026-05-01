package codegen

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/dropship-dev/craftgo/internal/config"
	"github.com/dropship-dev/craftgo/internal/semantic"
)

// mainData is the template input for `main.tmpl`. The umbrella
// routes.RegisterAll keeps the import set tiny — main.go references
// only `config`, `routes`, `middleware`, `svccontext`, and the
// runtime observability packages.
type mainData struct {
	ConfigImport     string
	RoutesImport     string
	MiddlewareImport string
	SvccontextImport string
	Middlewares      []string
	HasMiddlewares   bool
	// OperationName seeds otel.HTTPMiddleware's span name. Defaults to
	// the project's last package segment (e.g. `example` for
	// `github.com/dropship-dev/craftgo/example`) so traces self-label
	// without needing a manual edit.
	OperationName string
}

// GenerateMain scaffolds the project's main.go (`output.main`). The file
// is written once and skipped on subsequent gen runs so user-written
// boot code (extra middlewares, config loading, OTel SDK setup, etc.)
// survives regeneration.
//
// When the project declares no services the function is a no-op —
// there's no canonical RegisterRoutes target to wire.
//
// Setting `output.main: "-"` in the manifest opts the project out of
// scaffolding entirely — useful for test fixtures that ship their own
// httptest server and would collide with a generated `package main`.
func GenerateMain(pkg *semantic.Package, cfg *config.Config, projectRoot string) error {
	if pkg.Name == "" {
		return fmt.Errorf("package has no name")
	}
	if len(pkg.Services) == 0 {
		return nil
	}
	if cfg.Output.Main == "-" {
		return nil
	}
	dest := filepath.Join(projectRoot, cfg.Output.Main)
	if _, err := os.Stat(dest); err == nil {
		// Gen-once: don't clobber user changes.
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	data := buildMainData(pkg, cfg)
	formatted, err := renderGo(tmpl("main.tmpl"), data)
	if err != nil {
		return fmt.Errorf("render main.go: %w", err)
	}
	return os.WriteFile(dest, formatted, 0o644)
}

// GenerateProjectMain is the multi-package counterpart of
// [GenerateMain]: it scaffolds main.go using the union of services and
// middlewares from every package. The single shared umbrella
// `routes.RegisterAll` (emitted by [GenerateProjectRoutesUmbrella])
// continues to be the one-call wire-up so the template doesn't have
// to import per-package routes packages.
//
// As with the single-package variant, the file is gen-once and skipped
// when it already exists.
func GenerateProjectMain(proj *semantic.Project, cfg *config.Config, projectRoot string) error {
	if proj == nil {
		return nil
	}
	if cfg.Output.Main == "-" {
		return nil
	}
	// Skip when no package declares a service — same policy as the
	// single-package GenerateMain.
	hasService := false
	for _, p := range proj.Packages {
		if p != nil && len(p.Services) > 0 {
			hasService = true
			break
		}
	}
	if !hasService {
		return nil
	}
	dest := filepath.Join(projectRoot, cfg.Output.Main)
	if _, err := os.Stat(dest); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	data := buildProjectMainData(proj, cfg)
	formatted, err := renderGo(tmpl("main.tmpl"), data)
	if err != nil {
		return fmt.Errorf("render main.go: %w", err)
	}
	return os.WriteFile(dest, formatted, 0o644)
}

// buildProjectMainData unions every package's middleware names into
// one deterministic list. The umbrella RegisterAll already aggregates
// services so the template needs no further per-package wiring.
func buildProjectMainData(proj *semantic.Project, cfg *config.Config) mainData {
	d := mainData{
		ConfigImport:     goImportFromRel(cfg.Package, cfg.Output.Config),
		RoutesImport:     goImportFromRel(cfg.Package, cfg.Output.Routes),
		MiddlewareImport: goImportFromRel(cfg.Package, cfg.Output.Middleware),
		SvccontextImport: goImportFromRel(cfg.Package, fileDirRel(cfg.Output.Svccontext)),
		OperationName:    operationNameFor(cfg.Package),
	}
	seen := map[string]bool{}
	for _, p := range proj.Packages {
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
	return d
}

// buildMainData assembles the import paths + middleware list the
// template needs. Per-service routes are reached via the umbrella
// `routes.RegisterAll`, so main.go only imports the umbrella.
func buildMainData(pkg *semantic.Package, cfg *config.Config) mainData {
	d := mainData{
		ConfigImport:     goImportFromRel(cfg.Package, cfg.Output.Config),
		RoutesImport:     goImportFromRel(cfg.Package, cfg.Output.Routes),
		MiddlewareImport: goImportFromRel(cfg.Package, cfg.Output.Middleware),
		SvccontextImport: goImportFromRel(cfg.Package, fileDirRel(cfg.Output.Svccontext)),
		OperationName:    operationNameFor(cfg.Package),
	}
	d.Middlewares = sortedMiddlewareNames(pkg)
	d.HasMiddlewares = len(d.Middlewares) > 0
	return d
}

// operationNameFor extracts the last segment of a Go module path
// (`github.com/foo/myapp` → `myapp`) for use as the OTel span name.
// Falls back to a generic `api` when the input has no segments —
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
