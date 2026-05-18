package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// middlewareData is the template input for `middleware.tmpl`. One value
// is built per `middleware Name` declaration in the DSL. ParamList is
// the formatted Go-side parameter signature (e.g. `rps int, burst int`)
// derived from the DSL `middleware Name(rps: int = 100, burst: int = 200)`
// form; empty when the declaration has no params. DefaultsDoc is a
// commentary list of the declared defaults, surfaced in the scaffold
// header so users wiring main.go know which defaults the DSL chose.
type middlewareData struct {
	Name        string
	ParamList   string
	DefaultsDoc []string
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
//     Scaffold-only - gen-once; existing files are left alone.
//  2. The TYPE declaration list at `<svccontext-dir>/middlewares.go`.
//     Always overwritten - derived purely from the DSL.
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

// GenerateProjectMiddlewares emits the unified `svccontext/middlewares.go`
// + per-middleware scaffolds for every package in the project. Middleware
// names are global (the project resolver enforces uniqueness), so a
// single Middlewares struct embeds every declaration regardless of which
// package it lives in. Run ONCE per `craftgo gen` instead of per-package.
func GenerateProjectMiddlewares(proj *semantic.Project, cfg *config.Config, projectRoot string) error {
	names := projectSortedMiddlewareNames(proj)
	if err := writeMiddlewareFields(cfg, projectRoot, names); err != nil {
		return err
	}
	return writeProjectMiddlewareImpls(cfg, projectRoot, proj, names)
}

// projectSortedMiddlewareNames collects middleware decl names from every
// package in the project, deduplicates, and sorts. Cross-package
// uniqueness is already enforced at semantic time so no two packages
// can claim the same name; the dedupe is defensive.
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
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// writeProjectMiddlewareImpls emits scaffold files for every middleware
// in the project. Existing files survive - the framework only writes
// missing scaffolds so user edits in the impl body are preserved across
// `craftgo gen` runs.
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
		filename := kebabCase(name) + "-middleware.go"
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

// projectMiddlewareDecls flattens every package's middleware decls
// into a single name → MiddlewareDecl map. Names are project-globally
// unique (semantic phase enforces this), so the dedupe is defensive.
func projectMiddlewareDecls(proj *semantic.Project) map[string]*ast.MiddlewareDecl {
	out := map[string]*ast.MiddlewareDecl{}
	if proj == nil {
		return out
	}
	for _, pkg := range proj.Packages {
		if pkg == nil {
			continue
		}
		for n, md := range pkg.Middlewares {
			out[n] = md
		}
	}
	return out
}

// buildMiddlewareData fills the scaffold-template inputs from the
// DSL-declared params. When the middleware was declared without
// parens the scaffold falls back to the bare `New<Name>Middleware()`
// signature; with params the generated constructor's signature
// matches the DSL so wire-up in `main.go` doesn't need a second
// adapter layer.
func buildMiddlewareData(name string, md *ast.MiddlewareDecl) middlewareData {
	d := middlewareData{Name: name}
	if md == nil || len(md.Params) == 0 {
		return d
	}
	parts := make([]string, 0, len(md.Params))
	defaults := make([]string, 0, len(md.Params))
	for _, p := range md.Params {
		if p == nil || p.Type == nil {
			continue
		}
		parts = append(parts, p.Name+" "+GoTypeRef(p.Type))
		if p.Default != nil {
			defaults = append(defaults, fmt.Sprintf("%s = %s", p.Name, exprText(p.Default)))
		}
	}
	d.ParamList = strings.Join(parts, ", ")
	d.DefaultsDoc = defaults
	return d
}

// exprText renders a default-expression literal back to its source
// form so the scaffold comment surfaces the DSL-declared default in
// a shape the user recognises.
func exprText(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.StringLit:
		return fmt.Sprintf("%q", v.Value)
	case *ast.IntLit:
		return fmt.Sprintf("%d", v.Value)
	case *ast.FloatLit:
		return fmt.Sprintf("%g", v.Value)
	case *ast.BoolLit:
		if v.Value {
			return "true"
		}
		return "false"
	case *ast.DurationLit:
		return v.Text
	case *ast.SizeLit:
		return v.Text
	case *ast.IdentExpr:
		if v.Name != nil {
			return v.Name.String()
		}
	}
	return "?"
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
