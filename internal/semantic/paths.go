package semantic

// Path resolution validation. Runs after services are merged, so we
// have the final method list per service and can compute each method's
// final route by joining (basePath, @prefix, @group, methodPath). The
// pass surfaces five distinct issues:
//
//   - [CodePathBaseFormat]     - basePath malformed (warning).
//   - [CodePathCollision]      - two methods resolve to the same
//     VERB + path across services.
//   - [CodePathParamMissing]   - `{name}` in path but no matching
//     field binding in the request type.
//   - [CodePathParamOrphan]    - `@path` field with no corresponding
//     `{name}` segment.
//   - [CodePathHealthConflict] - declared route equals a reserved
//     health path.
//
// [ResolveRoute] below is the single route-computation authority for the
// whole pipeline: codegen's routes / OpenAPI / route-conflict emitters call
// it too, so the analyzer and the generated server cannot disagree on a route.

import (
	"maps"
	"slices"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/route"
)

// defaultHealthPaths is the runtime's auto-registered set, mirrored here so
// the analyser can flag collisions without importing the runtime package.
// TestHealthPathsMatchRuntime asserts these equal pkg/server's
// DefaultLivenessPath / DefaultReadinessPath, so the mirror cannot drift.
var defaultHealthPaths = []string{"/healthz", "/readyz"}

// checkPathResolution runs the four route-level validations described
// in the package doc comment plus the basePath format warning. The
// pass is idempotent and stateless beyond [analyzer.diags].
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

	type routeKey struct {
		verb string
		path string
	}
	type routeMeta struct {
		pos     lexer.Position
		service string
		method  string
	}
	seen := map[routeKey]routeMeta{}

	for svcName, si := range a.pkg.Services {
		for _, m := range si.Methods {
			rt := a.resolveMethodPath(si.Primary, m)
			verb := strings.ToUpper(m.Verb)

			if healthSet[rt] {
				a.diag(m.Pos, m.Pos, lexer.SeverityError, CodePathHealthConflict,
					"method %s.%s resolves to %s, which is a reserved health path",
					svcName, m.Name, rt)
			}

			if !a.opts.skipPathParamCheck {
				checkMethodPathParams(svcName, m, rt, a.pathParamEnv())
			}

			// Key by SHAPE so `/u/{id}` and `/u/{uid}` collide — they
			// register against the same net/http pattern at boot. The
			// displayed route keeps the literal form for the diagnostic.
			key := routeKey{verb: verb, path: route.Shape(rt)}
			if prev, dup := seen[key]; dup && prev.service != svcName {
				diag := a.diag(m.Pos, m.Pos, lexer.SeverityError, CodePathCollision,
					"method %s.%s resolves to %s %s, which already binds %s.%s",
					svcName, m.Name, verb, rt, prev.service, prev.method)
				diag.Related = related(prev.pos, "first declared here")
				continue
			}
			// Same-service duplicates are reported by checkServiceMethods -
			// don't double-fire.
			if _, dup := seen[key]; !dup {
				seen[key] = routeMeta{pos: m.Pos, service: svcName, method: m.Name}
			}
		}
	}
}

// checkProjectPathCollision is the cross-package twin of the route-collision
// scan in [analyzer.checkPathResolution]: two methods in DIFFERENT packages
// that resolve to the same VERB + route shape register the same net/http
// pattern, so the second registration panics at boot. The per-package pass
// only sees its own package's services (same-package pairs stay its job, and
// [checkServiceMethods] owns same-service duplicates); this pass reports just
// the cross-package pairs, so nothing double-fires. Packages and services
// iterate in sorted order so "first declared here" is deterministic.
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
				key := routeKey{verb: strings.ToUpper(m.Verb), path: route.Shape(rt)}
				prev, dup := seen[key]
				if !dup {
					seen[key] = routeMeta{pos: m.Pos, pkg: pkgName, service: svcName, method: m.Name}
					continue
				}
				if prev.pkg == pkgName {
					// Same package — the per-package pass owns the pair.
					continue
				}
				d := r.diag(m.Pos, lexer.SeverityError, CodePathCollision,
					"method %s.%s resolves to %s %s, which already binds %s.%s (package %s)",
					svcName, m.Name, key.verb, rt, prev.service, prev.method, prev.pkg)
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

// resolveMethodPath is the analyzer-bound shorthand for [ResolveRoute] with
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
// The two run modes differ only in their [pathParamEnv]: the per-package
// analyzer resolves names against its own package, while the project-
// level pass ([refResolver.checkProjectPathParams]) resolves qualified
// mixin / request names across packages - so `type Req { shared.IdHolder }`
// binds its `@path` field the same way the codegen binder does.
func checkMethodPathParams(svcName string, m *ast.Method, route string, env pathParamEnv) {
	pathParams := extractPathParams(route)
	// When the route declares `{param}` segments but the method has no
	// request struct, the generated logic signature drops to bare
	// `func() error` — path values land nowhere. Surface a warning so
	// authors realise they need to declare a request struct (or accept
	// that the path param is informational only). Downgraded from
	// error because many test fixtures legitimately use the no-request
	// pattern for routes that pass the param straight to a downstream
	// passthrough; tightening to error would regress those builds.
	if m.Request == nil {
		// Passthrough methods receive the raw http.ResponseWriter
		// and *http.Request, so path params land via `r.PathValue`
		// at the framework boundary — no struct binding is needed
		// and the diagnostic would be spurious for them.
		passthrough := false
		for _, d := range m.Decorators {
			if d != nil && d.Name == "passthrough" {
				passthrough = true
				break
			}
		}
		if len(pathParams) > 0 && !passthrough {
			env.emit(m.Pos, CodePathParamMissing,
				"method %s.%s: path declares %v but no request struct — path values won't reach logic. Declare a request struct with a `<name> string @path` (or matching field name) to bind.",
				svcName, m.Name, pathParams)
		}
		return
	}
	if len(pathParams) == 0 && m.Request.Name == nil {
		return
	}
	reqFields := requestPathFields(m, pathParams, env)
	if reqFields == nil {
		// Unknown / cross-package request type - placement / qualified-ref
		// pass owns the diagnostic; we silently skip rather than emit a
		// confusing missing-field error on a name we couldn't resolve.
		return
	}
	// Missing: route param has no field.
	for _, p := range pathParams {
		if !reqFields.has(p) {
			env.emit(m.Pos, CodePathParamMissing,
				"method %s.%s: path segment {%s} has no matching field in request type",
				svcName, m.Name, p)
		}
	}
	// Orphan: field claims @path explicitly but route lacks the segment.
	// Auto-bound fields don't fire orphan - they're just a regular field
	// that happens not to coincide with any path segment.
	for _, name := range reqFields.explicit {
		if !inSet(name, pathParams) {
			env.emit(m.Pos, CodePathParamOrphan,
				"method %s.%s: field %q has @path binding but route %s has no {%s} segment",
				svcName, m.Name, name, route, name)
		}
	}
}

// pathParamEnv abstracts the two run modes of [checkMethodPathParams]:
// how a (possibly qualified) type name resolves to its declaration, and
// where diagnostics are sent. The per-package analyzer resolves against
// its own package; the project resolver resolves across every package.
type pathParamEnv struct {
	// lookup resolves a type name - bare (`IdHolder`) or qualified
	// (`shared.IdHolder`) - to its declaration, or nil when unresolved.
	lookup func(name string) *ast.TypeDecl
	// emit records a path-param diagnostic (always SeverityError).
	emit func(pos lexer.Position, code, format string, args ...any)
}

// pathParamEnv builds the per-package environment: names resolve in the
// analyzer's own package, diagnostics land on a.diags.
func (a *analyzer) pathParamEnv() pathParamEnv {
	return pathParamEnv{
		lookup: func(name string) *ast.TypeDecl { return a.pkg.Types[name] },
		emit: func(pos lexer.Position, code, format string, args ...any) {
			a.diag(pos, pos, lexer.SeverityError, code, format, args...)
		},
	}
}

// checkProjectPathParams re-runs the `@path` segment ↔ field check with
// cross-package visibility. The per-package pass is muted under
// [Options.skipPathParamCheck] in project mode (it can't expand a mixin
// pulled from a sibling package), so this is the single emit site there.
// A request type and its mixins resolve across packages exactly as the
// codegen binder's [flattenFields] does, so the diagnostic agrees with
// what codegen will generate.
func (r *refResolver) checkProjectPathParams() {
	for pkgName, pkg := range r.proj.Packages {
		if pkg == nil {
			continue
		}
		current := pkgName
		env := pathParamEnv{
			lookup: func(name string) *ast.TypeDecl {
				if i := strings.LastIndexByte(name, '.'); i >= 0 {
					if p := r.proj.Packages[name[:i]]; p != nil {
						return p.Types[name[i+1:]]
					}
					return nil
				}
				if p := r.proj.Packages[current]; p != nil {
					return p.Types[name]
				}
				return nil
			},
			emit: func(pos lexer.Position, code, format string, args ...any) {
				r.diag(pos, lexer.SeverityError, code, format, args...)
			},
		}
		for svcName, si := range pkg.Services {
			if si == nil {
				continue
			}
			for _, m := range si.Methods {
				checkMethodPathParams(svcName, m, route.Resolve(r.basePath, si.Primary, m), env)
			}
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

// requestPathFields walks the method's request type and classifies
// fields against pathParams. Mixin members are expanded recursively
// through env.lookup so `type Req { Base  name string }` exposes Base's
// fields for path binding - the same view the codegen handler binder
// gets, including mixins pulled from a sibling package.
//
// Returns nil when the request type can't be resolved (unknown name) so
// the caller can skip path-param checks rather than emit a confusing
// missing-field error.
func requestPathFields(m *ast.Method, pathParams []string, env pathParamEnv) *pathParamSet {
	if m.Request == nil || m.Request.Name == nil {
		return nil
	}
	name := m.Request.Name.String()
	td := env.lookup(name)
	if td == nil {
		return nil
	}
	paramSet := map[string]bool{}
	for _, p := range pathParams {
		paramSet[p] = true
	}
	out := &pathParamSet{all: map[string]bool{}}
	// A qualified request type carries its package prefix so bare mixins
	// in its body resolve there, not against the current package.
	prefix := ""
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		prefix = name[:i]
	}
	walkBodyForPath(td, prefix, name, paramSet, out, map[string]bool{}, env)
	return out
}

// walkBodyForPath descends into td.Body, classifying fields and
// recursing into mixin targets resolved through env.lookup. `label` is
// the name td was reached by (the request type name, or a mixin ref);
// visited keys on it to prevent infinite recursion on cyclic mixin
// graphs (the mixin pass already reports the cycle) while keeping
// same-named types in different packages distinct.
func walkBodyForPath(td *ast.TypeDecl, prefix, label string, paramSet map[string]bool, out *pathParamSet, visited map[string]bool, env pathParamEnv) {
	if td == nil || visited[label] {
		return
	}
	visited[label] = true
	for _, mem := range td.Body {
		switch v := mem.(type) {
		case *ast.Field:
			name, hasExplicit := pathBindingName(v)
			if hasExplicit {
				out.all[name] = true
				out.explicit = append(out.explicit, name)
				continue
			}
			// A field auto-binds to a same-named segment ONLY when no other
			// wire decorator diverts it. `id string @query` on `/u/{id}`
			// rides the query string, so it does NOT cover the {id} segment
			// — mirror RequestFieldBinding (auto=false here) or the
			// path-coverage check passes while {id} stays unbound and the
			// emitted OpenAPI has no `in: path` parameter for it.
			if paramSet[v.Name] && !hasDivertingWireBinding(v.Decorators) {
				out.all[v.Name] = true
			}
		case *ast.Mixin:
			if v.Ref == nil || v.Ref.Name == nil {
				continue
			}
			// Resolve the mixin in the package it lives in: a qualified
			// ref names that package; a bare ref nested inside a foreign
			// mixin inherits that mixin's package (the prefix), so
			// `shared.XMid { XDeep }` resolves XDeep as `shared.XDeep`.
			key := v.Ref.Name.String()
			childPrefix := prefix
			if len(v.Ref.Name.Parts) == 2 {
				childPrefix = v.Ref.Name.Parts[0]
			} else if prefix != "" {
				key = prefix + "." + key
			}
			if next := env.lookup(key); next != nil {
				walkBodyForPath(next, childPrefix, key, paramSet, out, visited, env)
			}
		}
	}
}

// pathBindingName returns the path-segment name a field claims via
// `@path` and whether the field has the decorator at all. The custom
// override `@path("custom-name")` wins over the field's own identifier
// - that's the README contract.
// hasDivertingWireBinding reports whether a field carries a wire binding
// that routes it away from the path segment its name would otherwise
// auto-bind to (mirrors RequestFieldBinding returning auto=false). @path
// is handled by pathBindingName, so only the diverting bindings matter here.
func hasDivertingWireBinding(ds []*ast.Decorator) bool {
	for _, d := range ds {
		switch d.Name {
		case "query", "header", "cookie", "body", "form":
			return true
		}
	}
	return false
}

func pathBindingName(f *ast.Field) (string, bool) {
	for _, d := range f.Decorators {
		if d.Name != "path" {
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

// extractPathParams returns every `{name}` segment in route in source
// order. A malformed `{...` without closing `}` is silently ignored -
// the parser would already have rejected it.
func extractPathParams(route string) []string {
	var out []string
	for {
		i := strings.IndexByte(route, '{')
		if i < 0 {
			return out
		}
		j := strings.IndexByte(route[i:], '}')
		if j < 0 {
			return out
		}
		out = append(out, route[i+1:i+j])
		route = route[i+j+1:]
	}
}
