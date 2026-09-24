package golang

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/route"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// middlewareNames lists the @middlewares chain of m, a method of svc, outermost first, each name
// once and without its package qualifier; middleware names are unique across the project.
func middlewareNames(svc *semantic.ServiceInfo, m *ast.Method) []string {
	service, member, _ := svc.InheritedDecorators(m, "middlewares")
	var names []string
	for _, d := range slices.Concat(service, member) {
		for _, n := range ast.ArgNames(d) {
			if name := n.Value[strings.LastIndexByte(n.Value, '.')+1:]; !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	return names
}

// buildHandlerCall renders the handler argument of srv.Handle, wrapped in server.WithLimits
// when m declares @timeout or @maxBodySize.
func buildHandlerCall(m *ast.Method, transportAlias string) (call string, needsTime bool) {
	core := transportAlias + "." + m.Name + "(svcCtx)"
	lit, usesTime, ok := methodLimitsLiteral(m)
	if ok {
		core = "server.WithLimits(" + core + ", " + lit + ")"
	}
	return core, usesTime
}

// buildMiddlewareArgs renders mws as `svcCtx.A, svcCtx.B`; srv.Handle runs the first outermost.
func buildMiddlewareArgs(mws []string) string {
	if len(mws) == 0 {
		return ""
	}
	parts := make([]string, len(mws))
	for i, name := range mws {
		parts[i] = "svcCtx." + name
	}
	return strings.Join(parts, ", ")
}

// methodLimitsLiteral renders m's @timeout and @maxBodySize as a server.Limits literal; ok is
// false when m declares neither.
func methodLimitsLiteral(m *ast.Method) (lit string, usesTime, ok bool) {
	var fields []string
	if d, ok := semantic.DurationArg(firstArg(m.Decorators, "timeout")); ok {
		fields = append(fields, "Timeout: "+formatDurationGo(d))
		usesTime = true
	}
	if n, _ := semantic.SizeArg(firstArg(m.Decorators, "maxBodySize")); n > 0 {
		fields = append(fields, fmt.Sprintf("MaxBodySize: %d", n))
	}
	if len(fields) == 0 {
		return "", false, false
	}
	return "server.Limits{" + strings.Join(fields, ", ") + "}", usesTime, true
}

// formatDurationGo renders d in the largest unit that divides it (`30 * time.Second`).
func formatDurationGo(d time.Duration) string {
	switch {
	case d%time.Hour == 0:
		return fmt.Sprintf("%d * time.Hour", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%d * time.Minute", d/time.Minute)
	case d%time.Second == 0:
		return fmt.Sprintf("%d * time.Second", d/time.Second)
	case d%time.Millisecond == 0:
		return fmt.Sprintf("%d * time.Millisecond", d/time.Millisecond)
	}
	return fmt.Sprintf("%d * time.Nanosecond", d.Nanoseconds())
}

// routeEntry is one `srv.Handle(Pattern, HandlerCall, Middlewares)` line of routes.tmpl.
type routeEntry struct {
	Pattern     string
	Method      string
	HandlerCall string
	Middlewares string
}

// routesData is the template input for routes.tmpl.
type routesData struct {
	Package          string
	Service          string
	TransportImports []transportImport
	SvccontextImport string
	Routes           []routeEntry
	NeedsTime        bool
}

// transportImport is one aliased transport-package import in a routes file.
type transportImport struct {
	Alias string
	Path  string
}

// generateRoutes writes one output.routes/<segment>/routes.go per segment pkg's services occupy;
// services sharing a segment through @group share its RegisterRoutes.
func generateRoutes(pkg *semantic.Package, cfg *config.Config, projectRoot string) error {
	if pkg.Name == "" {
		return fmt.Errorf("package has no name")
	}
	dirs := routeSegments(pkg, cfg)
	for _, seg := range slices.Sorted(maps.Keys(dirs)) {
		if err := generateRoutesForSegment(seg, dirs[seg], pkg, cfg, projectRoot); err != nil {
			return err
		}
	}
	return nil
}

// segContribution is one service's methods under one @group ("" when ungrouped).
type segContribution struct {
	svcName string
	svc     *semantic.ServiceInfo
	group   string
}

// routeSegments maps each output segment of pkg to its contributors in service order; the
// analyser rejects a segment shared across DSL packages or repeating a method name.
func routeSegments(pkg *semantic.Package, cfg *config.Config) map[string][]segContribution {
	out := map[string][]segContribution{}
	for _, svcName := range pkg.ServiceNames() {
		svc := pkg.Services[svcName]
		for _, g := range distinctGroups(svc) {
			seg := outputSegFor(svcName, g, cfg.Output.FileCase)
			out[seg] = append(out[seg], segContribution{svcName: svcName, svc: svc, group: g})
		}
	}
	return out
}

// generateProjectRoutesUmbrella writes output.routes/routes.go, whose RegisterAll calls every
// segment's RegisterRoutes; no file is written when no service has a method.
func generateProjectRoutesUmbrella(proj *semantic.Project, cfg *config.Config, projectRoot string) error {
	type svcEntry struct {
		name    string
		pkgName string
		group   string
		seg     string
	}
	var entries []svcEntry
	for pkgName, p := range proj.Packages {
		if pkgName == "" || p == nil {
			continue
		}
		for _, svcName := range p.ServiceNames() {
			for _, g := range distinctGroups(p.Services[svcName]) {
				entries = append(entries, svcEntry{name: svcName, pkgName: pkgName, group: g, seg: outputSegFor(svcName, g, cfg.Output.FileCase)})
			}
		}
	}
	if len(entries) == 0 {
		return nil
	}
	// Service names are project-unique, but a service has one entry per group.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].name != entries[j].name {
			return entries[i].name < entries[j].name
		}
		return entries[i].group < entries[j].group
	})

	dir := filepath.Join(projectRoot, cfg.Output.Routes)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data := routesAllData{
		SvccontextImport: goImportFromRel(cfg.Package, fileDirRel(cfg.Output.Svccontext)),
	}
	// Services sharing a segment share its RegisterRoutes, so it is called once.
	seen := map[string]bool{}
	for _, e := range entries {
		if seen[e.seg] {
			continue
		}
		seen[e.seg] = true
		data.Imports = append(data.Imports, makeRoutesAllImport(cfg, e.name, e.group, e.seg))
	}
	formatted, err := renderGo(tmpl("routes-all.tmpl"), data)
	if err != nil {
		return fmt.Errorf("render routes-all: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "routes.go"), formatted, 0o644)
}

// routesAllImport is one aliased routes-package import of the umbrella routes.go.
type routesAllImport struct {
	Alias string
	Path  string
}

// makeRoutesAllImport builds the umbrella's import of one segment's routes package.
func makeRoutesAllImport(cfg *config.Config, name, group, seg string) routesAllImport {
	return routesAllImport{
		Alias: servicePackage(name) + groupAliasSuffix(group) + "routes",
		Path:  goImportFromRel(cfg.Package, cfg.Output.Routes) + "/" + seg,
	}
}

// routesAllData is the template input for `routes-all.tmpl`.
type routesAllData struct {
	Imports          []routesAllImport
	SvccontextImport string
}

// generateRoutesForSegment writes the routes.go of segment seg: each contributor's methods in
// source order, all through the segment's one transport package.
func generateRoutesForSegment(seg string, contribs []segContribution, pkg *semantic.Package, cfg *config.Config, projectRoot string) error {
	if len(contribs) == 0 {
		return nil
	}
	dir := filepath.Join(projectRoot, cfg.Output.Routes, filepath.FromSlash(seg))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	lead := contribs[0]
	alias := transportAlias(lead.group)
	data := routesData{
		Package:          servicePkgName(pkg.Name, lead.svcName),
		Service:          contributorLabel(contribs),
		SvccontextImport: importPathsForGroup(cfg, pkg, lead.svcName, "").Svccontext,
		TransportImports: []transportImport{{
			Alias: alias,
			Path:  importPathsForGroup(cfg, pkg, lead.svcName, lead.group).Transport,
		}},
	}
	for _, c := range contribs {
		for _, m := range c.svc.Methods {
			if semantic.MethodGroupOf(c.svc, m) != c.group {
				continue
			}
			full := route.Resolve(cfg.OpenAPI.BasePath, c.svc.Primary, m)
			mws := middlewareNames(c.svc, m)
			call, needsTime := buildHandlerCall(m, alias)
			if needsTime {
				data.NeedsTime = true
			}
			data.Routes = append(data.Routes, routeEntry{
				Pattern:     httpVerb(m.Verb) + " " + full,
				Method:      m.Name,
				HandlerCall: call,
				Middlewares: buildMiddlewareArgs(mws),
			})
		}
	}
	formatted, err := renderGo(tmpl("routes.tmpl"), data)
	if err != nil {
		return fmt.Errorf("render routes: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "routes.go"), formatted, 0o644)
}

// contributorLabel joins the contributing service names for the routes.go doc ("A, B and C").
func contributorLabel(contribs []segContribution) string {
	seen := map[string]bool{}
	var names []string
	for _, c := range contribs {
		if seen[c.svcName] {
			continue
		}
		seen[c.svcName] = true
		names = append(names, c.svcName)
	}
	switch len(names) {
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}
