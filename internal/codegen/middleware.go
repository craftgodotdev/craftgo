package codegen

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/dropship-dev/craftgo/internal/config"
	"github.com/dropship-dev/craftgo/internal/semantic"
)

// middlewareData is the template input for `middleware.tmpl`. One value
// is built per `middleware Name` declaration in the DSL.
type middlewareData struct {
	Name string
}

// middlewareFieldsData is the input for `middleware-fields.tmpl`. The
// type declared by that template lives next to svccontext.go and lists
// one server.Middleware field per declaration.
type middlewareFieldsData struct {
	Names []string
}

// GenerateMiddlewares emits two artefacts per `middleware Name` block:
//
//  1. The IMPLEMENTATION at `<output.middleware>/<kebab-name>-middleware.go`.
//     Scaffold-only — gen-once; existing files are left alone.
//  2. The TYPE declaration list at `<svccontext-dir>/middlewares.go`.
//     Always overwritten — derived purely from the DSL.
//
// Users embed the generated `Middlewares` struct into their own
// ServiceContext, then assign each field to a concrete impl in main.go.
// Routes consume the middleware via the embedded fields directly, so no
// runtime name registry lookup is needed.
func GenerateMiddlewares(pkg *semantic.Package, cfg *config.Config, projectRoot string) error {
	if pkg.Name == "" {
		return fmt.Errorf("package has no name")
	}
	names := sortedMiddlewareNames(pkg)
	if err := writeMiddlewareFields(cfg, projectRoot, names); err != nil {
		return err
	}
	return writeMiddlewareImpls(cfg, projectRoot, pkg, names)
}

// writeMiddlewareFields emits svccontext/middlewares.go (overwrite). When
// the DSL declares no middlewares the file is removed so a stale
// declaration cannot leak between runs.
func writeMiddlewareFields(cfg *config.Config, projectRoot string, names []string) error {
	dir := filepath.Join(projectRoot, fileDirRel(cfg.Output.Svccontext))
	dest := filepath.Join(dir, "middlewares.go")
	if len(names) == 0 {
		_ = os.Remove(dest)
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	formatted, err := renderGo(tmpl("middleware-fields.tmpl"), middlewareFieldsData{Names: names})
	if err != nil {
		return fmt.Errorf("render middlewares.go: %w", err)
	}
	return os.WriteFile(dest, formatted, 0o644)
}

// writeMiddlewareImpls scaffolds one impl file per middleware. Existing
// files survive untouched (gen-once).
func writeMiddlewareImpls(cfg *config.Config, projectRoot string, _ *semantic.Package, names []string) error {
	dir := filepath.Join(projectRoot, cfg.Output.Middleware)
	if len(names) == 0 {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tpl := tmpl("middleware.tmpl")
	for _, name := range names {
		filename := kebabCase(name) + "-middleware.go"
		dest := filepath.Join(dir, filename)
		if _, err := os.Stat(dest); err == nil {
			continue
		}
		formatted, err := renderGo(tpl, middlewareData{Name: name})
		if err != nil {
			return fmt.Errorf("render middleware %s: %w", name, err)
		}
		if err := os.WriteFile(dest, formatted, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// sortedMiddlewareNames returns the package's middleware declarations
// in deterministic order.
func sortedMiddlewareNames(pkg *semantic.Package) []string {
	out := make([]string, 0, len(pkg.Middlewares))
	for n := range pkg.Middlewares {
		out = append(out, n)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
