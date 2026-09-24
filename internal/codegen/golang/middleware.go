package golang

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// middlewareData is the template input for middleware.tmpl.
type middlewareData struct {
	Name string
}

// middlewareFieldsData is the template input for middleware-fields.tmpl.
type middlewareFieldsData struct {
	Names []string
}

// generateProjectMiddlewares writes middlewares.go beside output.svccontext, one Middlewares
// field per declared middleware, and each middleware's gen-once scaffold under output.middleware.
func generateProjectMiddlewares(proj *semantic.Project, cfg *config.Config, projectRoot string) error {
	names := projectSortedMiddlewareNames(proj)
	if err := writeMiddlewareFields(cfg, projectRoot, names); err != nil {
		return err
	}
	return writeProjectMiddlewareImpls(cfg, projectRoot, proj, names)
}

func projectSortedMiddlewareNames(proj *semantic.Project) []string {
	seen := map[string]struct{}{}
	for _, pkg := range proj.Packages {
		if pkg == nil {
			continue
		}
		for name := range pkg.Middlewares {
			seen[name] = struct{}{}
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// writeProjectMiddlewareImpls writes each middleware's scaffold unless its file exists.
func writeProjectMiddlewareImpls(cfg *config.Config, projectRoot string, proj *semantic.Project, names []string) error {
	if len(names) == 0 {
		return nil
	}
	dir := filepath.Join(projectRoot, cfg.Output.Middleware)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tpl := tmpl("middleware.tmpl")
	declByName := projectMiddlewareDecls(proj)
	for _, name := range names {
		filename := idents.FileNameWords(cfg.Output.FileCase, append(idents.SplitFieldName(name), "middleware")) + ".go"
		dest := filepath.Join(dir, filename)
		if _, err := os.Stat(dest); err == nil {
			continue
		}
		data := buildMiddlewareData(name, declByName[name])
		formatted, err := renderGo(tpl, data)
		if err != nil {
			return fmt.Errorf("render middleware %s: %w", name, err)
		}
		if err := os.WriteFile(dest, formatted, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// projectMiddlewareDecls maps every package's middleware declarations by name.
func projectMiddlewareDecls(proj *semantic.Project) map[string]*ast.MiddlewareDecl {
	out := map[string]*ast.MiddlewareDecl{}
	if proj == nil {
		return out
	}
	for _, pkg := range proj.Packages {
		if pkg == nil {
			continue
		}
		maps.Copy(out, pkg.Middlewares)
	}
	return out
}

func buildMiddlewareData(name string, _ *ast.MiddlewareDecl) middlewareData {
	return middlewareData{Name: name}
}

// writeMiddlewareFields writes middlewares.go even with no middleware, since svccontext.go embeds
// its Middlewares type.
func writeMiddlewareFields(cfg *config.Config, projectRoot string, names []string) error {
	dir := filepath.Join(projectRoot, fileDirRel(cfg.Output.Svccontext))
	dest := filepath.Join(dir, "middlewares.go")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	formatted, err := renderGo(tmpl("middleware-fields.tmpl"), middlewareFieldsData{Names: names})
	if err != nil {
		return fmt.Errorf("render middlewares.go: %w", err)
	}
	return os.WriteFile(dest, formatted, 0o644)
}
