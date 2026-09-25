package golang

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
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
	Imports          []goImport
	SvccontextImport string
	Routes           []routeEntry
	NeedsTime        bool
}

// generateRoutes writes one output.routes/<segment>/routes.go per segment pkg's services occupy;
// services sharing a segment through @group share its RegisterRoutes.
func generateRoutes(pkg *semantic.Package, cfg *config.Config, projectRoot string) error {
	if pkg.Name == "" {
		return fmt.Errorf("package has no name")
	}
	bySeg := map[string][]segment{}
	for s := range segments(pkg, cfg.Output.FileCase) {
		bySeg[s.dir] = append(bySeg[s.dir], s)
	}
	// The analyser rejects a segment shared across DSL packages or repeating a method name.
	for _, dir := range slices.Sorted(maps.Keys(bySeg)) {
		if err := generateRoutesForSegment(bySeg[dir], cfg, projectRoot); err != nil {
			return err
		}
	}
	return nil
}

// generateProjectRoutesUmbrella writes output.routes/routes.go, whose RegisterAll calls every
// segment's RegisterRoutes; no file is written when no service has a method.
func generateProjectRoutesUmbrella(proj *semantic.Project, cfg *config.Config, projectRoot string) error {
	entries := slices.Collect(projectSegments(proj, cfg.Output.FileCase))
	if len(entries) == 0 {
		return nil
	}
	// Service names are project-unique, but a service has one entry per group.
	slices.SortFunc(entries, func(a, b segment) int {
		return cmp.Or(cmp.Compare(a.name, b.name), cmp.Compare(a.group, b.group))
	})
	out := outputsOf(cfg)
	data := routesAllData{SvccontextImport: out.svccontext.pkg}
	imports := newImportSet(nil, goImport{}, routesNames)
	for _, s := range entries {
		path := out.routes.sub(s.dir).pkg
		// Services sharing a segment share its RegisterRoutes, so it is called once.
		if imports.has(path) {
			continue
		}
		data.Imports = append(data.Imports, goImport{Alias: imports.add(servicePackage(s.name)+groupAliasSuffix(s.group)+"routes", path), Path: path})
	}
	return writeGo(out.routes.at(projectRoot, "routes.go"), tmpl("routes-all.tmpl"), data)
}

// routesAllData is the template input for `routes-all.tmpl`; Imports are in call order.
type routesAllData struct {
	Imports          []goImport
	SvccontextImport string
}

// generateRoutesForSegment writes the routes.go of the segment contribs share: each contributor's
// methods in source order, all through the segment's one transport package.
func generateRoutesForSegment(contribs []segment, cfg *config.Config, projectRoot string) error {
	lead := contribs[0]
	out := outputsOf(cfg)
	imports := newImportSet(nil, goImport{}, routesNames)
	alias := imports.add(transportAlias(lead.group), out.transport.sub(lead.dir).pkg)
	data := routesData{
		Package:          servicePkgName(lead.pkg.Name, lead.name),
		Service:          contributorLabel(contribs),
		SvccontextImport: out.svccontext.pkg,
		Imports:          imports.imports(),
	}
	for _, c := range contribs {
		for m := range c.methods() {
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
	return writeGo(out.routes.sub(lead.dir).at(projectRoot, "routes.go"), tmpl("routes.tmpl"), data)
}

// contributorLabel joins the contributing service names for the routes.go doc ("A, B and C").
func contributorLabel(contribs []segment) string {
	seen := map[string]bool{}
	var names []string
	for _, c := range contribs {
		if seen[c.name] {
			continue
		}
		seen[c.name] = true
		names = append(names, c.name)
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
