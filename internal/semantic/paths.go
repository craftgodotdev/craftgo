package semantic

// Path resolution validation. Runs after services are merged, so we
// have the final method list per service and can compute each method's
// final route by joining (basePath, @prefix, @group, methodPath). The
// pass surfaces five distinct issues:
//
//   - [CodePathBaseFormat]     - basePath malformed (warning).
//   - [CodePathCollision]      - two methods resolve to the same
//     VERB + path across services or packages
//     ([refResolver.checkProjectPathCollision]).
//   - [CodePathParamMissing]   - `{name}` in path but no matching
//     field binding in the request type.
//   - [CodePathParamOrphan]    - `@path` field with no corresponding
//     `{name}` segment.
//   - [CodePathHealthConflict] - declared route equals a reserved
//     health path.
//
// [route.Resolve] is the single route-computation authority for the
// whole pipeline: codegen's routes / OpenAPI / route-conflict emitters call
// it too, so the analyzer and the generated server cannot disagree on a route.

import (
	"maps"
	"slices"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/route"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// defaultHealthPaths is the runtime's auto-registered set, mirrored here so
// the analyser can flag collisions without importing the runtime package.
// TestHealthPathsMatchRuntime asserts these equal pkg/server's
// DefaultLivenessPath / DefaultReadinessPath, so the mirror cannot drift.
var defaultHealthPaths = []string{"/healthz", "/readyz"}

// checkPathResolution runs the basePath format warning, the reserved
// health-path check, and the `{param}` ↔ field check for every method.
// The pass is idempotent and stateless beyond [analyzer.diags].
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

// checkProjectPathCollision flags two methods that resolve to the same
// VERB + route shape: they register the same net/http pattern, so the
// second registration panics at boot. Pairs across services and across
// packages are both reported here; a same-service duplicate is reported
// by [analyzer.checkServiceMethods]. Packages and services iterate in
// sorted order so "first declared here" is deterministic.
func (r *refResolver) checkProjectPathCollision() {
	type routeKey struct {
		verb string
		path string
	}
	type routeMeta struct {
		pos     lexer.Position
		pkg     string
		service string
		method  string
	}
	pkgNames := slices.Sorted(maps.Keys(r.proj.Packages))
	seen := map[routeKey]routeMeta{}
	for _, pkgName := range pkgNames {
		pkg := r.proj.Packages[pkgName]
		if pkg == nil {
			continue
		}
		svcNames := slices.Sorted(maps.Keys(pkg.Services))
		for _, svcName := range svcNames {
			si := pkg.Services[svcName]
			if si == nil {
				continue
			}
			for _, m := range si.Methods {
				rt := route.Resolve(r.basePath, si.Primary, m)
				// Key by SHAPE so `/u/{id}` and `/u/{uid}` collide - they
				// register against the same net/http pattern at boot. The
				// displayed route keeps the literal form for the diagnostic.
				key := routeKey{verb: strings.ToUpper(m.Verb), path: route.Shape(rt)}
				prev, dup := seen[key]
				if !dup {
					seen[key] = routeMeta{pos: m.Pos, pkg: pkgName, service: svcName, method: m.Name}
					continue
				}
				if prev.pkg == pkgName && prev.service == svcName {
					continue
				}
				var d *Diagnostic
				if prev.pkg == pkgName {
					d = r.diag(m.Pos, lexer.SeverityError, CodePathCollision,
						"method %s.%s resolves to %s %s, which already binds %s.%s",
						svcName, m.Name, key.verb, rt, prev.service, prev.method)
				} else {
					d = r.diag(m.Pos, lexer.SeverityError, CodePathCollision,
						"method %s.%s resolves to %s %s, which already binds %s.%s (package %s)",
						svcName, m.Name, key.verb, rt, prev.service, prev.method, prev.pkg)
				}
				d.Related = related(prev.pos, "first declared here")
			}
		}
	}
}

// checkBasePathFormat emits a warning when the configured basePath
// doesn't match the canonical shape: empty, OR starts with `/`, no
// trailing slash, no `//`. Codegen normalises in either direction so
// this is informational rather than blocking.
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
	// We don't have a position for the manifest value (it's parsed by
	// the config loader, not the DSL parser), so use the zero
	// position. The IDE renders this as a project-level diagnostic.
	a.diag(lexer.Position{}, lexer.Position{}, lexer.SeverityWarning,
		CodePathBaseFormat,
		"basePath %q is malformed: %s - codegen will normalise but please fix the manifest",
		bp, bad)
}

// resolveMethodPath is the analyzer-bound shorthand for [route.Resolve] with
// the configured basePath applied.
func (a *analyzer) resolveMethodPath(svc *ast.ServiceDecl, m *ast.Method) string {
	return route.Resolve(a.opts.BasePath, svc, m)
}

// checkMethodPathParams validates that `{name}` segments in route
// match field bindings in the method's request type. Two issues fire:
//
//   - missing: a path segment with no field to bind to;
//   - orphan:  a `@path` / `@path("x")` field with no `{x}` in route.
//
// Two rules govern the matching, mirroring the codegen's auto-bind
// logic in `internal/codegen.collectBindings`:
//
//  1. An explicit `@path` decorator binds the field. Custom name
//     `@path("custom")` wins over the field's identifier.
//  2. A field whose NAME matches a path segment auto-binds, even
//     without `@path`. (`type GetUserReq { id string }` paired with
//     `/users/{id}` is the canonical example.)
//
// Auto-bound fields are NOT subject to the orphan check - only
// explicitly-decorated ones, since a bare-named field that happens
// to not match the path is just a regular query/body field.
//
// The request type and its mixins resolve across packages exactly as the
// codegen binder does, so `type Req { shared.IdHolder }` binds its `@path`
// field the same way.
func (a *analyzer) checkMethodPathParams(svcName string, m *ast.Method, rt string) {
	pathParams := route.Vars(rt)
	// When the route declares `{param}` segments but the method has no
	// request struct, the generated logic signature drops to bare
	// `func() error` - path values land nowhere. Surface a warning so
	// authors realise they need to declare a request struct (or accept
	// that the path param is informational only). Downgraded from
	// error because many test fixtures legitimately use the no-request
	// pattern for routes that pass the param straight to a downstream
	// passthrough; tightening to error would regress those builds.
	if m.Request == nil {
		// A raw-request method (`@rawRequest` / `@passthrough`) reads
		// path values straight off the *http.Request via `r.PathValue`,
		// so no struct binding is needed and the diagnostic would be
		// spurious for it.
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
		// Unknown / cross-package request type - placement / qualified-ref
		// pass owns the diagnostic; we silently skip rather than emit a
		// confusing missing-field error on a name we couldn't resolve.
		return
	}
	// Missing: route param has no field.
	for _, p := range pathParams {
		if !reqFields.has(p) {
			a.diag(m.Pos, m.Pos, lexer.SeverityError, CodePathParamMissing,
				"method %s.%s: path segment {%s} has no matching field in request type",
				svcName, m.Name, p)
		}
	}
	// Orphan: field claims @path explicitly but route lacks the segment.
	// Auto-bound fields don't fire orphan - they're just a regular field
	// that happens not to coincide with any path segment.
	for _, name := range reqFields.explicit {
		if !inSet(name, pathParams) {
			a.diag(m.Pos, m.Pos, lexer.SeverityError, CodePathParamOrphan,
				"method %s.%s: field %q has @path binding but route %s has no {%s} segment",
				svcName, m.Name, name, rt, name)
		}
	}
}

// pathParamSet is the set of names that the request type advertises
// as path-bindable, plus the subset that did so via an explicit
// `@path` decorator. The orphan check uses `explicit` so an auto-
// bound field that doesn't actually appear in the path doesn't
// false-positive.
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

// requestPathFields classifies the fields of m's request type against
// pathParams. Mixin members are expanded so `type Req { Base  name string }`
// exposes Base's fields for path binding - the same view the codegen
// handler binder gets, including mixins pulled from a sibling package.
//
// Returns nil when the request type can't be resolved (unknown name) so
// the caller can skip path-param checks rather than emit a confusing
// missing-field error.
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
		// A field auto-binds to a same-named segment ONLY when no other
		// wire decorator diverts it. `id string @query` on `/u/{id}`
		// rides the query string, so it does NOT cover the {id} segment
		// - mirror RequestFieldBinding (auto=false here) or the
		// path-coverage check passes while {id} stays unbound and the
		// emitted OpenAPI has no `in: path` parameter for it.
		if paramSet[f.Name] && !hasDivertingWireBinding(f.Decorators) {
			out.all[f.Name] = true
		}
	}
	return out
}

// hasDivertingWireBinding reports whether a field carries a wire binding
// that routes it away from the path segment its name would otherwise
// auto-bind to (mirrors RequestFieldBinding returning auto=false). @path
// is handled by pathBindingName, so only the diverting bindings matter here.
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

// pathBindingName returns the path-segment name a field claims via
// `@path` and whether the field has the decorator at all. The custom
// override `@path("custom-name")` wins over the field's own identifier.
func pathBindingName(f *ast.Field) (string, bool) {
	for _, d := range f.Decorators {
		if d == nil || d.Name != wire.BindingPath {
			continue
		}
		if len(d.Args) > 0 {
			// An empty wire-name arg (`@path("")`) falls back to the field
			// name, mirroring WireName so the path-param check and the
			// binder agree on the segment a field claims.
			if s, ok := d.Args[0].Value.(*ast.StringLit); ok && s.Value != "" {
				return s.Value, true
			}
		}
		return f.Name, true
	}
	return "", false
}
