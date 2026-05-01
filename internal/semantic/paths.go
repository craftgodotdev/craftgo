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
// A slim copy of `methodFullPath` lives here rather than imported
// from codegen: semantic stays codegen-free so the LSP can reuse it
// without pulling template machinery.

import (
	"strings"

	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/lexer"
)

// defaultHealthPaths is the runtime's auto-registered set, mirrored
// here so the analyser can flag collisions without depending on the
// runtime package. Keep in sync with `pkg/server` defaults.
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
			route := a.resolveMethodPath(si.Primary, m)
			verb := strings.ToUpper(m.Verb)

			if healthSet[route] {
				a.diag(m.Pos, m.Pos, lexer.SeverityError, CodePathHealthConflict,
					"method %s.%s resolves to %s, which is a reserved health path",
					svcName, m.Name, route)
			}

			a.checkMethodPathParams(svcName, m, route)

			key := routeKey{verb: verb, path: route}
			if prev, dup := seen[key]; dup && prev.service != svcName {
				diag := a.diag(m.Pos, m.Pos, lexer.SeverityError, CodePathCollision,
					"method %s.%s resolves to %s %s, which already binds %s.%s",
					svcName, m.Name, verb, route, prev.service, prev.method)
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

// resolveMethodPath joins basePath + @prefix + @group + methodPath
// using the same rules as `internal/codegen.methodFullPath`. Empty
// segments are dropped; consecutive slashes are collapsed; the result
// always starts with `/`. When the method has no inline path the
// fallback is the kebab-cased method name (matching codegen).
func (a *analyzer) resolveMethodPath(svc *ast.ServiceDecl, m *ast.Method) string {
	parts := []string{}
	if a.opts.BasePath != "" {
		parts = append(parts, a.opts.BasePath)
	}
	if p := decoratorString(svc, "prefix"); p != "" {
		parts = append(parts, p)
	}
	if g := decoratorString(svc, "group"); g != "" {
		parts = append(parts, g)
	}
	if m.Path != nil {
		parts = append(parts, PathString(m.Path))
	} else {
		parts = append(parts, "/"+camelToKebab(m.Name))
	}
	joined := strings.Join(parts, "/")
	for strings.Contains(joined, "//") {
		joined = strings.ReplaceAll(joined, "//", "/")
	}
	if joined == "" || joined[0] != '/' {
		joined = "/" + joined
	}
	if len(joined) > 1 {
		joined = strings.TrimRight(joined, "/")
	}
	return joined
}

// decoratorString returns the first string-literal positional arg of
// `@name(...)` on the service decl, or "" when absent. Used to read
// `@prefix` and `@group` without depending on codegen helpers.
func decoratorString(svc *ast.ServiceDecl, name string) string {
	if svc == nil {
		return ""
	}
	for _, d := range svc.Decorators {
		if d.Name != name || len(d.Args) == 0 {
			continue
		}
		if s, ok := d.Args[0].Value.(*ast.StringLit); ok {
			return s.Value
		}
	}
	return ""
}

// camelToKebab is the local copy of the codegen helper, kept here so
// semantic doesn't import codegen. PascalCase / camelCase → kebab,
// preserving common-initialism boundaries (`HTTPStream` →
// `http-stream`).
//
// Hot path: rune-by-rune transform. Builder keeps the per-character
// append allocation-free.
func camelToKebab(s string) string {
	var sb strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 && needsHyphen(s, i) {
				sb.WriteByte('-')
			}
			sb.WriteRune(r - 'A' + 'a')
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// needsHyphen reports whether position i in s is the start of a new
// "word" for kebab-conversion: either the previous char was lowercase
// (camel-case boundary) or the next char is lowercase while we're
// in a run of uppercase (acronym → word boundary).
func needsHyphen(s string, i int) bool {
	prev := s[i-1]
	if prev >= 'a' && prev <= 'z' {
		return true
	}
	if i+1 < len(s) {
		next := s[i+1]
		if next >= 'a' && next <= 'z' {
			return true
		}
	}
	return false
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
func (a *analyzer) checkMethodPathParams(svcName string, m *ast.Method, route string) {
	pathParams := extractPathParams(route)
	// No request type means the user pulls path params via `r.PathValue`
	// in their logic-side code - codegen permits this, so we don't flag
	// missing bindings. We still keep walking when a request DOES exist
	// so explicit `@path` orphans are reported.
	if m.Request == nil {
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
				svcName, m.Name, name, route, name)
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
// fields against pathParams. Mixin members are expanded recursively so
// `type Req { Base  name string }` exposes Base's fields for path
// binding - same view the codegen handler binder gets.
//
// Returns nil when the request type can't be resolved (cross-package
// or unknown name) so the caller can skip path-param checks rather
// than emit a confusing missing-field error.
func (a *analyzer) requestPathFields(m *ast.Method, pathParams []string) *pathParamSet {
	if m.Request == nil || m.Request.Name == nil || len(m.Request.Name.Parts) != 1 {
		return nil
	}
	td, ok := a.pkg.Types[m.Request.Name.Parts[0]]
	if !ok {
		return nil
	}
	paramSet := map[string]bool{}
	for _, p := range pathParams {
		paramSet[p] = true
	}
	out := &pathParamSet{all: map[string]bool{}}
	a.walkBodyForPath(td, paramSet, out, map[string]bool{})
	return out
}

// walkBodyForPath descends into td.Body, classifying fields and
// recursing into mixin targets. visited prevents infinite recursion
// on cyclic mixin graphs (the mixin pass already reports the cycle).
func (a *analyzer) walkBodyForPath(td *ast.TypeDecl, paramSet map[string]bool, out *pathParamSet, visited map[string]bool) {
	if visited[td.Name] {
		return
	}
	visited[td.Name] = true
	for _, mem := range td.Body {
		switch v := mem.(type) {
		case *ast.Field:
			name, hasExplicit := pathBindingName(v)
			if hasExplicit {
				out.all[name] = true
				out.explicit = append(out.explicit, name)
				continue
			}
			if paramSet[v.Name] {
				out.all[v.Name] = true
			}
		case *ast.Mixin:
			if v.Ref == nil || v.Ref.Name == nil || len(v.Ref.Name.Parts) != 1 {
				continue
			}
			if next, ok := a.pkg.Types[v.Ref.Name.Parts[0]]; ok {
				a.walkBodyForPath(next, paramSet, out, visited)
			}
		}
	}
}

// pathBindingName returns the path-segment name a field claims via
// `@path` and whether the field has the decorator at all. The custom
// override `@path("custom-name")` wins over the field's own identifier
// - that's the README contract.
func pathBindingName(f *ast.Field) (string, bool) {
	for _, d := range f.Decorators {
		if d.Name != "path" {
			continue
		}
		if len(d.Args) > 0 {
			if s, ok := d.Args[0].Value.(*ast.StringLit); ok {
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
