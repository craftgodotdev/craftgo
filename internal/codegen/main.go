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
// only `routes`, `middleware`, and `svccontext`.
type mainData struct {
	RoutesImport     string
	MiddlewareImport string
	SvccontextImport string
	Middlewares      []string
	HasMiddlewares   bool
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

// buildMainData assembles the import paths + middleware list the
// template needs. Per-service routes are reached via the umbrella
// `routes.RegisterAll`, so main.go only imports the umbrella.
func buildMainData(pkg *semantic.Package, cfg *config.Config) mainData {
	d := mainData{
		RoutesImport:     goImportFromRel(cfg.Package, cfg.Output.Routes),
		MiddlewareImport: goImportFromRel(cfg.Package, cfg.Output.Middleware),
		SvccontextImport: goImportFromRel(cfg.Package, fileDirRel(cfg.Output.Svccontext)),
	}
	d.Middlewares = sortedMiddlewareNames(pkg)
	d.HasMiddlewares = len(d.Middlewares) > 0
	return d
}
