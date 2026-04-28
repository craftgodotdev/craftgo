package codegen

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/config"
	"github.com/dropship-dev/craftgo/internal/semantic"
)

// middlewareNames returns the union of middleware identifiers declared
// on the service (via service-level `@middlewares(...)`) and the method
// (via method-level `@middlewares(...)`). Service-level middlewares run
// outermost so they wrap every method. Order within each level matches
// the source.
func middlewareNames(m *ast.Method, svc *ast.ServiceDecl) []string {
	var names []string
	if svc != nil {
		names = append(names, extractMiddlewareNames(svc.Decorators)...)
	}
	names = append(names, extractMiddlewareNames(m.Decorators)...)
	return names
}

// buildHandlerCall produces the Go expression that lands as the second
// argument to `srv.HandleFunc`. Middlewares are wrapped LEFT-TO-RIGHT
// so the first name in the slice ends up outermost, matching how
// readers expect to see the chain ("Auth wraps everything else").
func buildHandlerCall(methodName string, mws []string) string {
	core := "handler." + methodName + "Handler(svcCtx)"
	if len(mws) == 0 {
		return core
	}
	for i := len(mws) - 1; i >= 0; i-- {
		core = "svcCtx." + mws[i] + "(" + core + ")"
	}
	return core
}

// extractMiddlewareNames pulls the identifier arguments out of every
// `@middlewares(...)` decorator in ds. Non-identifier args are skipped
// because the runtime registry is keyed by name.
func extractMiddlewareNames(ds []*ast.Decorator) []string {
	var out []string
	for _, d := range ds {
		if d.Name != "middlewares" {
			continue
		}
		for _, a := range d.Args {
			if id, ok := a.Value.(*ast.IdentExpr); ok {
				out = append(out, id.Name.String())
			}
		}
	}
	return out
}

// routeEntry is one row in the routes table emitted by `routes.tmpl`.
// HandlerCall is the fully-formed Go expression the template emits as
// the second argument to `srv.HandleFunc` — already wrapped in any
// service- and method-level middlewares declared via `@middlewares`.
type routeEntry struct {
	Pattern     string
	Method      string
	HandlerCall string
}

// routesData is the template input for `routes.tmpl`.
type routesData struct {
	Package          string
	Service          string
	HandlerImport    string
	SvccontextImport string
	Routes           []routeEntry
}

// GenerateRoutes emits one `routes.go` per service under
// `<output.routes>/<servicePackage>/` PLUS a top-level
// `<output.routes>/routes.go` that exposes `RegisterAll(srv, svcCtx)` —
// the one-call wire-up consumed by main.go. Both layers are
// regenerated on every gen because they're derived purely from the
// DSL service set.
func GenerateRoutes(pkg *semantic.Package, cfg *config.Config, projectRoot string) error {
	if pkg.Name == "" {
		return fmt.Errorf("package has no name")
	}
	for _, svcName := range sortedServices(pkg) {
		svc := pkg.Services[svcName]
		if err := generateRoutesFor(svcName, svc, pkg, cfg, projectRoot); err != nil {
			return err
		}
	}
	return generateRoutesAll(pkg, cfg, projectRoot)
}

// routesAllImport is one row in the umbrella routes.go's import
// block. The Alias is the package alias used at the call site so the
// generated code compiles even when several services would otherwise
// resolve to the same Go package name.
type routesAllImport struct {
	Alias string
	Path  string
}

// routesAllData is the template input for `routes-all.tmpl`.
type routesAllData struct {
	Imports          []routesAllImport
	SvccontextImport string
}

// generateRoutesAll emits the top-level umbrella routes file. Skipped
// when the package declares no services — the umbrella has nothing to
// wire and an empty `routes` package would shadow `pkg/server.routes`-
// style identifiers in user code.
func generateRoutesAll(pkg *semantic.Package, cfg *config.Config, projectRoot string) error {
	names := sortedServices(pkg)
	if len(names) == 0 {
		return nil
	}
	dir := filepath.Join(projectRoot, cfg.Output.Routes)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data := routesAllData{
		SvccontextImport: goImportFromRel(cfg.Package, fileDirRel(cfg.Output.Svccontext)),
	}
	for _, name := range names {
		data.Imports = append(data.Imports, routesAllImport{
			Alias: ServicePackage(name) + "routes",
			Path:  goImportFromRel(cfg.Package, cfg.Output.Routes) + "/" + ServiceDir(name),
		})
	}
	formatted, err := renderGo(tmpl("routes-all.tmpl"), data)
	if err != nil {
		return fmt.Errorf("render routes-all: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "routes.go"), formatted, 0o644)
}

// generateRoutesFor emits the routes.go file for a single service.
func generateRoutesFor(svcName string, svc *semantic.ServiceInfo, pkg *semantic.Package, cfg *config.Config, projectRoot string) error {
	imps := importPathsFor(cfg, pkg, svcName)
	dir := filepath.Join(projectRoot, cfg.Output.Routes, ServiceDir(svcName))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data := routesData{
		Package:          ServicePackage(svcName),
		Service:          svcName,
		HandlerImport:    imps.Handler,
		SvccontextImport: imps.Svccontext,
	}
	for _, m := range svc.Methods {
		full := methodFullPath(cfg.OpenAPI.BasePath, svc.Primary, m)
		mws := middlewareNames(m, svc.Primary)
		data.Routes = append(data.Routes, routeEntry{
			Pattern:     httpVerb(m.Verb) + " " + full,
			Method:      m.Name,
			HandlerCall: buildHandlerCall(m.Name, mws),
		})
	}
	formatted, err := renderGo(tmpl("routes.tmpl"), data)
	if err != nil {
		return fmt.Errorf("render routes: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "routes.go"), formatted, 0o644)
}
