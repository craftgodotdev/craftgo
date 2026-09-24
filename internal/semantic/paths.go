package semantic

import (
	"maps"
	"slices"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/route"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// defaultHealthPaths are the health routes pkg/server registers by default.
var defaultHealthPaths = []string{"/healthz", "/readyz"}

// checkPathResolution checks the basePath format, and each method's route
// against the health paths and its request fields.
func (a *analyzer) checkPathResolution() {
	a.checkBasePathFormat()

	healths := a.opts.HealthPaths
	if len(healths) == 0 {
		healths = defaultHealthPaths
	}
	healthSet := map[string]bool{}
	for _, h := range healths {
		healthSet[h] = true
	}
	for _, svcName := range slices.Sorted(maps.Keys(a.pkg.Services)) {
		si := a.pkg.Services[svcName]
		for _, m := range si.Methods {
			rt := a.resolveMethodPath(si.Primary, m)
			if healthSet[rt] {
				a.diag(m.Pos, m.Pos, lexer.SeverityError, CodePathHealthConflict,
					"method %s.%s resolves to %s, which is a reserved health path",
					svcName, m.Name, rt)
			}
			a.checkMethodPathParams(svcName, m, rt)
		}
	}
}

// checkProjectPathCollision reports method pairs that net/http's ServeMux
// refuses to register together: one verb with the same route shape, or
// overlapping shapes with neither more specific. A same-shape pair within
// one service is left to [analyzer.checkServiceMethods].
func (r *refResolver) checkProjectPathCollision() {
	type routeEntry struct {
		verb, route, shape   string
		pos                  lexer.Position
		pkg, service, method string
	}
	var entries []routeEntry
	for _, pkgName := range slices.Sorted(maps.Keys(r.proj.Packages)) {
		pkg := r.proj.Packages[pkgName]
		if pkg == nil {
			continue
		}
		for _, svcName := range slices.Sorted(maps.Keys(pkg.Services)) {
			si := pkg.Services[svcName]
			if si == nil {
				continue
			}
			for _, m := range si.Methods {
				rt := route.Resolve(r.basePath, si.Primary, m)
				entries = append(entries, routeEntry{
					verb: strings.ToUpper(m.Verb), route: rt, shape: route.Shape(rt),
					pos: m.Pos, pkg: pkgName, service: svcName, method: m.Name,
				})
			}
		}
	}
	// Same shape: each later declaration is reported against the first.
	type routeKey struct{ verb, shape string }
	first := map[routeKey]routeEntry{}
	for _, e := range entries {
		key := routeKey{e.verb, e.shape}
		prev, dup := first[key]
		if !dup {
			first[key] = e
			continue
		}
		if prev.pkg == e.pkg && prev.service == e.service {
			continue
		}
		d := r.diag(e.pos, lexer.SeverityError, CodePathCollision,
			"method %s.%s resolves to %s %s, which already binds %s.%s%s",
			e.service, e.method, e.verb, e.route, prev.service, prev.method, packageNote(prev.pkg, e.pkg))
		d.Related = related(prev.pos, "first declared here")
	}
	// Overlapping shapes: one diagnostic per pair, at the later declaration.
	for i := 1; i < len(entries); i++ {
		for j := 0; j < i; j++ {
			a, b := entries[i], entries[j]
			if a.verb != b.verb || a.shape == b.shape || !route.PatternsConflict(a.route, b.route) {
				continue
			}
			d := r.diag(a.pos, lexer.SeverityError, CodePathCollision,
				"method %s.%s resolves to %s %s, which overlaps %s %s of %s.%s%s: both match the same paths and neither is more specific, so net/http rejects the pair at startup; give one route a distinct literal segment or move the variable to @query",
				a.service, a.method, a.verb, a.route, b.verb, b.route, b.service, b.method, packageNote(b.pkg, a.pkg))
			d.Related = related(b.pos, "overlaps this route")
		}
	}
}

// packageNote renders " (package X)" when the other declaration lives in a
// package other than own.
func packageNote(other, own string) string {
	if other == own {
		return ""
	}
	return " (package " + other + ")"
}

// checkBasePathFormat warns when a non-empty basePath lacks the leading
// `/`, ends with `/` or contains `//`.
func (a *analyzer) checkBasePathFormat() {
	bp := a.opts.BasePath
	if bp == "" {
		return
	}
	bad := ""
	switch {
	case !strings.HasPrefix(bp, "/"):
		bad = "must start with `/`"
	case len(bp) > 1 && strings.HasSuffix(bp, "/"):
		bad = "must not end with `/`"
	case strings.Contains(bp, "//"):
		bad = "must not contain `//`"
	}
	if bad == "" {
		return
	}
	// The manifest value has no source position.
	a.diag(lexer.Position{}, lexer.Position{}, lexer.SeverityWarning,
		CodePathBaseFormat,
		"basePath %q is malformed: %s - codegen will normalise but please fix the manifest",
		bp, bad)
}

// resolveMethodPath is [route.Resolve] with the configured basePath.
func (a *analyzer) resolveMethodPath(svc *ast.ServiceDecl, m *ast.Method) string {
	return route.Resolve(a.opts.BasePath, svc, m)
}

// checkMethodPathParams reports a `{name}` in rt that no request field
// binds, by `@path` or by its name, and an explicit `@path` field with no
// segment in rt. A raw request reads its path values itself.
func (a *analyzer) checkMethodPathParams(svcName string, m *ast.Method, rt string) {
	pathParams := route.Vars(rt)
	if m.Request == nil {
		rawReq, _ := wire.RawSides(m.Decorators)
		if len(pathParams) > 0 && !rawReq {
			a.diag(m.Pos, m.Pos, lexer.SeverityError, CodePathParamMissing,
				"method %s.%s: path declares %v but no request struct - path values won't reach logic. Declare a request struct with a `<name> string @path` (or matching field name) to bind.",
				svcName, m.Name, pathParams)
		}
		return
	}
	if len(pathParams) == 0 && m.Request.Name == nil {
		return
	}
	reqFields := a.requestPathFields(m, pathParams)
	if reqFields == nil {
		return // an unresolved request type is reported by the reference checks
	}
	for _, p := range pathParams {
		if !reqFields.has(p) {
			a.diag(m.Pos, m.Pos, lexer.SeverityError, CodePathParamMissing,
				"method %s.%s: path segment {%s} has no matching field in request type",
				svcName, m.Name, p)
		}
	}
	for _, name := range reqFields.explicit {
		if !inSet(name, pathParams) {
			a.diag(m.Pos, m.Pos, lexer.SeverityError, CodePathParamOrphan,
				"method %s.%s: field %q has @path binding but route %s has no {%s} segment",
				svcName, m.Name, name, rt, name)
		}
	}
}

// pathParamSet is the segment names a request binds; explicit holds those
// bound by `@path`.
type pathParamSet struct {
	all      map[string]bool
	explicit []string
}

// has reports whether the name is bindable.
func (s *pathParamSet) has(name string) bool {
	if s == nil {
		return false
	}
	return s.all[name]
}

// requestPathFields collects the segment names m's request fields bind,
// mixin fields included; nil when the request type does not resolve.
func (a *analyzer) requestPathFields(m *ast.Method, pathParams []string) *pathParamSet {
	td, fields := a.requestFields(m)
	if td == nil {
		return nil
	}
	paramSet := map[string]bool{}
	for _, p := range pathParams {
		paramSet[p] = true
	}
	out := &pathParamSet{all: map[string]bool{}}
	for _, pf := range fields {
		f := pf.Field
		name, hasExplicit := pathBindingName(f)
		if hasExplicit {
			out.all[name] = true
			out.explicit = append(out.explicit, name)
			continue
		}
		// A same-named field binds its segment unless another binding claims it.
		if paramSet[f.Name] && !hasDivertingWireBinding(f.Decorators) {
			out.all[f.Name] = true
		}
	}
	return out
}

// hasDivertingWireBinding reports a binding decorator other than @path.
func hasDivertingWireBinding(ds []*ast.Decorator) bool {
	for _, d := range ds {
		if d == nil {
			continue
		}
		switch d.Name {
		case wire.BindingQuery, wire.BindingHeader, wire.BindingCookie, wire.BindingBody, wire.BindingForm:
			return true
		}
	}
	return false
}

// pathBindingName returns the segment an `@path` field binds, its non-empty
// argument or else its own name, and whether the field has `@path`.
func pathBindingName(f *ast.Field) (string, bool) {
	for _, d := range f.Decorators {
		if d == nil || d.Name != wire.BindingPath {
			continue
		}
		if len(d.Args) > 0 {
			if s, ok := d.Args[0].Value.(*ast.StringLit); ok && s.Value != "" {
				return s.Value, true
			}
		}
		return f.Name, true
	}
	return "", false
}
