package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/route"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// middlewareNames returns the chain of middleware identifiers for one
// method. The chain is assembled outermost-first so codegen wraps the
// handler in the same order a reader sees the decorators:
//
//  1. Primary service-level `@middlewares(...)`
//  2. Extend-block-level `@middlewares(...)` (decorators marked
//     Propagated=true that the semantic merge copied onto the method)
//  3. Method-level `@middlewares(...)` (decorators with
//     Propagated=false that the user wrote directly above the method)
//
// `@ignoreMiddleware` on a method drops layers 1 + 2 - the inherited
// chain - so the method starts fresh from layer 3. This implements the
// clear-then-append pattern documented in
// docs/guide/decorators.md#service-level-decorators-and-inheritance.
//
// A name repeated across layers is kept ONCE, at its outermost position.
// The layers append rather than override, so re-stating an inherited
// middleware is easy to do by accident - `@middlewares(Auth)` on both the
// primary service and an `extend` block of it, say - and the duplicate is
// never what the author meant: the generated route would list
// `svcCtx.Auth, svcCtx.Auth` and run the middleware twice per request.
// Keeping the FIRST occurrence preserves the inherited layer's outer
// position, which is the guarantee service-level decorators exist to give.
// This mirrors the dedup the same inherited chains already get in the
// OpenAPI emitters ([operationTags], [dedupSecurity]).
func middlewareNames(m *ast.Method, svc *ast.ServiceDecl) []string {
	ignore := false
	for _, d := range m.Decorators {
		if d != nil && !d.Propagated && d.Name == "ignoreMiddleware" {
			ignore = true
			break
		}
	}
	var names []string
	seen := map[string]bool{}
	appendNames := func(ns []string) {
		for _, n := range ns {
			if seen[n] {
				continue
			}
			seen[n] = true
			names = append(names, n)
		}
	}
	if svc != nil && !ignore {
		appendNames(extractMiddlewareNames(svc.Decorators))
	}
	for _, d := range m.Decorators {
		if d == nil || d.Name != "middlewares" {
			continue
		}
		if d.Propagated && ignore {
			continue
		}
		appendNames(extractMiddlewareNames([]*ast.Decorator{d}))
	}
	return names
}

// buildHandlerCall produces the Go expression that lands as the SECOND
// argument to `srv.Handle` - the handler itself, with `server.WithLimits`
// applied when the method declares `@timeout` or `@maxBodySize`. The
// middleware chain is rendered separately as variadic args by
// [buildMiddlewareArgs] so the route line stays flat regardless of
// chain depth.
//
// Limits wrap the handler INSIDE the middleware chain so middlewares
// see the timeout/body-cap-bound handler - the timeout cancels the
// downstream work, not the middleware's own bookkeeping.
func buildHandlerCall(m *ast.Method, transportAlias string) string {
	core := transportAlias + "." + m.Name + "(svcCtx)"
	if lit, ok := methodLimitsLiteral(m); ok {
		core = "server.WithLimits(" + core + ", " + lit + ")"
	}
	return core
}

// buildMiddlewareArgs produces the variadic-middleware-arg list that
// the routes template splices after the handler. Returns the comma-
// separated `svcCtx.A, svcCtx.B, svcCtx.C` form (no leading comma)
// when the chain is non-empty, otherwise "" so the template skips the
// argument entirely.
//
// The first name in mws is the OUTERMOST frame at runtime;
// server.Handle's variadic wrap iterates right-to-left so the chain
// reads top-to-bottom in the generated route line.
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

// methodLimitsLiteral renders a `server.Limits{...}` Go-source struct
// literal from the method's decorators, or returns ("", false) when
// neither `@timeout` nor `@maxBodySize` is present. Passthrough
// methods opt out of `@timeout` because the framework hands the
// writer/request to logic verbatim and `http.TimeoutHandler` would
// cut whatever stream logic decides to produce; `@maxBodySize`
// still applies (the body cap fires at read time, not response time).
func methodLimitsLiteral(m *ast.Method) (string, bool) {
	passthrough := hasPassthroughDecorator(m.Decorators)
	var fields []string
	if !passthrough {
		if d := durationDecoratorArg(m.Decorators, "timeout"); d != "" {
			fields = append(fields, "Timeout: "+d)
		}
	}
	if n := sizeDecoratorArg(m.Decorators, "maxBodySize"); n > 0 {
		fields = append(fields, fmt.Sprintf("MaxBodySize: %d", n))
	}
	if len(fields) == 0 {
		return "", false
	}
	return "server.Limits{" + strings.Join(fields, ", ") + "}", true
}

// durationDecoratorArg returns the Go-source expression for a
// duration argument like `@timeout(30s)`. Supports both
// DurationLit (preferred) and bare integers (interpreted as seconds
// per the README's "bare number → seconds" rule). Empty string means
// the decorator is absent or carries an unsupported literal.
func durationDecoratorArg(ds []*ast.Decorator, name string) string {
	for _, d := range ds {
		if d.Name != name || len(d.Args) == 0 {
			continue
		}
		switch v := d.Args[0].Value.(type) {
		case *ast.DurationLit:
			if dur, ok := parseDurationText(v.Text); ok {
				return formatDurationGo(dur)
			}
		case *ast.IntLit:
			return fmt.Sprintf("%d * time.Second", v.Value)
		}
	}
	return ""
}

// sizeDecoratorArg returns the byte count for a size argument like
// `@maxBodySize(10MB)` or `@maxBodySize(1024)`. Reuses the size parser
// from the file-validator codegen path.
func sizeDecoratorArg(ds []*ast.Decorator, name string) int64 {
	for _, d := range ds {
		if d.Name != name || len(d.Args) == 0 {
			continue
		}
		if n, ok := sizeArg(d.Args[0]); ok {
			return n
		}
	}
	return 0
}

// parseDurationText converts a DSL duration literal (e.g. "30s",
// "1.5h") into a time.Duration. Wraps the stdlib parser so we don't
// duplicate the suffix matrix.
func parseDurationText(text string) (time.Duration, bool) {
	// `µs` and `us` are both DSL-legal; ParseDuration accepts both.
	d, err := time.ParseDuration(text)
	if err != nil {
		return 0, false
	}
	return d, true
}

// formatDurationGo emits a duration as a Go-source expression,
// preferring the largest unit that divides cleanly so the generated
// routes file reads naturally ("30 * time.Second" beats "30000000000").
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

// extractMiddlewareNames pulls the identifier arguments out of every
// `@middlewares(...)` decorator in ds and returns the BARE name for
// each - the package prefix in `pkg.Name` is dropped because every
// middleware lands flat on svccontext (the project resolver already
// guarantees names are unique across packages).
func extractMiddlewareNames(ds []*ast.Decorator) []string {
	var out []string
	for _, d := range ds {
		if d.Name != "middlewares" {
			continue
		}
		for _, a := range d.Args {
			for _, v := range ast.DecoratorArgValues(a) {
				if id, ok := v.(*ast.IdentExpr); ok {
					parts := id.Name.Parts
					if len(parts) == 0 {
						continue
					}
					out = append(out, parts[len(parts)-1])
				}
			}
		}
	}
	return out
}

// routeEntry is one row in the routes table emitted by `routes.tmpl`.
// HandlerCall is the bare handler expression (plus optional
// `server.WithLimits` wrap when @timeout/@maxBodySize is declared);
// Middlewares is the variadic-arg list (`svcCtx.A, svcCtx.B`) the
// template splices AFTER the handler so the call reads flat:
//
//	srv.Handle("POST /x", handler, svcCtx.A, svcCtx.B)
//
// Empty Middlewares means the method opted out of the inherited
// chain via `@ignoreMiddleware` and declared no replacement, so the
// template skips the trailing comma + args entirely.
type routeEntry struct {
	Pattern     string
	Method      string
	HandlerCall string
	Middlewares string
}

// routesData is the template input for `routes.tmpl`. NeedsTime tells
// the template whether to import "time"; we set it when at least one
// route emits a duration literal so the generated file stays clean
// for projects that don't use timeout decorators.
type routesData struct {
	Package string
	Service string
	// TransportImports is one entry per distinct @group the service's
	// methods live in (the ungrouped root plus each group folder). Each
	// carries its own import alias so several group packages coexist in
	// one routes file. Empty when the service declares no methods.
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

// GenerateRoutes emits one `routes.go` per service under
// `<output.routes>/<servicePackage>/` PLUS a top-level
// `<output.routes>/routes.go` that exposes `RegisterAll(srv, svcCtx)` -
// the one-call wire-up consumed by main.go. Both layers are
// regenerated on every gen because they're derived purely from the
// DSL service set.
//
// Single-package callers should keep using this entry point. Multi-
// package projects call [GeneratePerServiceRoutes] per package and
// [GenerateProjectRoutesUmbrella] once for the project so the
// umbrella aggregates services from every package.
func GenerateRoutes(pkg *semantic.Package, cfg *config.Config, projectRoot string) error {
	if err := GeneratePerServiceRoutes(pkg, cfg, projectRoot); err != nil {
		return err
	}
	return generateRoutesAll(pkg, cfg, projectRoot)
}

// GeneratePerServiceRoutes emits the per-directory `routes.go` files;
// the umbrella is left to a project-level pass. Used by the multi-
// package CLI flow so each package's services contribute to a single
// shared umbrella rather than overwriting each other.
//
// The unit of emission is the OUTPUT DIRECTORY, not the service: only
// one `routes.go` can live in a folder, and `@group` deliberately lets
// several services share one. Every service landing in a directory
// contributes its methods to that directory's single RegisterRoutes.
func GeneratePerServiceRoutes(pkg *semantic.Package, cfg *config.Config, projectRoot string) error {
	if pkg.Name == "" {
		return fmt.Errorf("package has no name")
	}
	dirs := routeSegments(pkg, cfg)
	for _, seg := range sortedKeys(dirs) {
		if err := generateRoutesForSegment(seg, dirs[seg], pkg, cfg, projectRoot); err != nil {
			return err
		}
	}
	return nil
}

// segContribution is one service's share of an output directory: the
// service, and the @group that routed it there (""= its own directory).
// A service reaching one directory from several blocks that resolve to
// the same group appears once.
type segContribution struct {
	svcName string
	svc     *semantic.ServiceInfo
	group   string
}

// routeSegments maps each output segment the package occupies to the
// services contributing to it, in sorted service order so the emitted
// route table is stable run to run. Sharing a segment is legal and
// merges; the analyser has already rejected the two shapes a merge
// cannot express (contributors from different DSL packages, and two
// contributors declaring the same method name).
func routeSegments(pkg *semantic.Package, cfg *config.Config) map[string][]segContribution {
	out := map[string][]segContribution{}
	for _, svcName := range sortedServices(pkg) {
		svc := pkg.Services[svcName]
		for _, g := range distinctGroups(svc) {
			seg := outputSegFor(svcName, g, cfg.Output.FileCase)
			out[seg] = append(out[seg], segContribution{svcName: svcName, svc: svc, group: g})
		}
	}
	return out
}

// GenerateProjectRoutesUmbrella emits the top-level
// `<output.routes>/routes.go` that exposes `RegisterAll(srv, svcCtx)`,
// aggregating every service from every DSL package in the project.
// When no package declares a service the file is skipped - calling
// `RegisterAll` from main.go would also be a no-op.
func GenerateProjectRoutesUmbrella(proj *semantic.Project, cfg *config.Config, projectRoot string) error {
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
		for _, svcName := range sortedServices(p) {
			// One candidate entry per (service, group); the dedupe below
			// collapses them to one per output directory, which is the unit
			// routes.go is actually emitted for.
			for _, g := range distinctGroups(p.Services[svcName]) {
				entries = append(entries, svcEntry{name: svcName, pkgName: pkgName, group: g, seg: outputSegFor(svcName, g, cfg.Output.FileCase)})
			}
		}
	}
	if len(entries) == 0 {
		return nil
	}
	// Stable iteration order: by (service name, group). Service names are
	// project-unique after merging, but one service contributes one entry PER
	// GROUP - without the group tie-break the equal-name entries land in map
	// iteration order and the emitted file differs run to run.
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
	// Dedupe by output directory AFTER sorting, so the surviving entry is
	// the deterministic first claimant. Several services may share a
	// segment; their routes already merged into that folder's single
	// RegisterRoutes, and calling it once per service would re-register
	// every pattern it holds.
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

// routesAllImport is one row in the umbrella routes.go's import
// block. The Alias is the package alias used at the call site so the
// generated code compiles even when several services would otherwise
// resolve to the same Go package name.
type routesAllImport struct {
	Alias string
	Path  string
}

// makeRoutesAllImport builds the aliased import for one (service, group) routes
// hub. Both umbrella emitters (per-package and project-wide) use this so the
// alias / path formula lives in one place.
func makeRoutesAllImport(cfg *config.Config, name, group, seg string) routesAllImport {
	return routesAllImport{
		Alias: ServicePackage(name) + groupAliasSuffix(group) + "routes",
		Path:  goImportFromRel(cfg.Package, cfg.Output.Routes) + "/" + seg,
	}
}

// routesAllData is the template input for `routes-all.tmpl`.
type routesAllData struct {
	Imports          []routesAllImport
	SvccontextImport string
}

// generateRoutesAll emits the top-level umbrella routes file. Skipped
// when the package declares no services - the umbrella has nothing to
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
	// One import per output DIRECTORY, not per service: routes are emitted
	// per folder and several services may share one, so a per-service loop
	// would call the same RegisterRoutes twice and mount every pattern in
	// it twice - which http.ServeMux rejects with a panic at startup. Names
	// and groups are already sorted, so the first claimant of a segment is
	// deterministic.
	seen := map[string]bool{}
	for _, name := range names {
		for _, g := range distinctGroups(pkg.Services[name]) {
			seg := outputSegFor(name, g, cfg.Output.FileCase)
			if seen[seg] {
				continue
			}
			seen[seg] = true
			data.Imports = append(data.Imports, makeRoutesAllImport(cfg, name, g, seg))
		}
	}
	formatted, err := renderGo(tmpl("routes-all.tmpl"), data)
	if err != nil {
		return fmt.Errorf("render routes-all: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "routes.go"), formatted, 0o644)
}

// generateRoutesForSegment emits the single routes.go that serves one
// output directory, registering the methods every contributing service
// put there. An ungrouped service is the one-contributor case and emits
// exactly what it always did, at its own service directory.
//
// All contributors share the directory's transport package, so the file
// carries ONE transport import; its alias comes from the first
// contributor so the common single-service file is unchanged. Routes are
// laid out contributor by contributor, each service's methods in source
// order.
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
		Package:          ServicePkgName(pkg.Name, lead.svcName),
		Service:          contributorLabel(contribs),
		SvccontextImport: importPathsForGroup(cfg, pkg, lead.svcName, "").Svccontext,
		TransportImports: []transportImport{{
			Alias: alias,
			Path:  importPathsForGroup(cfg, pkg, lead.svcName, lead.group).Transport,
		}},
	}
	for _, c := range contribs {
		groups := methodGroups(c.svc)
		for _, m := range c.svc.Methods {
			if groups[m.Name] != c.group {
				continue
			}
			full := route.Resolve(cfg.OpenAPI.BasePath, c.svc.Primary, m)
			mws := middlewareNames(m, c.svc.Primary)
			call := buildHandlerCall(m, alias)
			if strings.Contains(call, "time.") {
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

// contributorLabel names the services a routes.go covers, for its doc
// comment. One service reads exactly as before ("wires every Foo
// endpoint"); a shared directory lists every contributor.
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
