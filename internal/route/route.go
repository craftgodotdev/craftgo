// Package route is the leaf authority for craftgo's two path namespaces and
// the wall between them. The URL side: how a method's final route is assembled
// (base path + @prefix + method path), the string form of a DSL path, the shape
// key two colliding routes share, and net/http's pattern-overlap rule. The disk
// side: `@group`, which decides the output segment a block's generated files
// land under and never contributes to the URL. The analyzer, the routes/OpenAPI
// emitters, and the route-conflict detector all read these - one implementation,
// so the route and the directory the editor diagnoses are byte-for-byte the ones
// the generated server mounts and codegen writes.
package route

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
)

// Resolve joins the OpenAPI base path, the service's @prefix, and the
// method's own path into the single absolute route the server registers and
// the OpenAPI document advertises. Empty segments are dropped, consecutive
// slashes collapse, the result always begins with '/', and a pathless method
// falls back to its kebab-cased name ("Ping" → "/ping"). @group is absent on
// purpose - it nests generated files on disk, never the URL.
//
// This is THE route authority: the analyzer's path checks and every codegen
// emitter (routes, OpenAPI paths, route-conflict detection) call it, so the
// route the editor diagnoses is byte-for-byte the route the server mounts.
func Resolve(basePath string, svc *ast.ServiceDecl, m *ast.Method) string {
	parts := []string{}
	if basePath != "" {
		parts = append(parts, basePath)
	}
	if p := ServicePrefix(svc); p != "" {
		parts = append(parts, p)
	}
	if m.Path != nil {
		parts = append(parts, PathString(m.Path))
	} else {
		parts = append(parts, "/"+idents.KebabCase(m.Name))
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

func PathString(p *ast.Path) string {
	if p == nil {
		return ""
	}
	var sb strings.Builder
	for _, s := range p.Segments {
		sb.WriteByte('/')
		if s.Param {
			sb.WriteByte('{')
			sb.WriteString(s.Literal)
			sb.WriteByte('}')
		} else {
			sb.WriteString(s.Literal)
		}
	}
	return sb.String()
}

// Shape replaces every variable segment of a resolved route with `{}`, so
// routes that differ only in variable names compare equal - they register
// the same net/http pattern.
func Shape(route string) string {
	segs := splitRouteSegments(route)
	for i, seg := range segs {
		if _, ok := varName(seg); ok {
			segs[i] = "{}"
		}
	}
	return "/" + strings.Join(segs, "/")
}

// Vars returns the `{name}` variable segments of a route, prefix or path
// string, in order. This is the one rule every layer applies to decide
// which segments bind a field and which patterns overlap.
func Vars(route string) []string {
	var out []string
	for _, seg := range splitRouteSegments(route) {
		if name, ok := varName(seg); ok {
			out = append(out, name)
		}
	}
	return out
}

// varName returns the name of a `{name}` segment - the only variable form
// the parser produces.
func varName(seg string) (string, bool) {
	if len(seg) > 2 && seg[0] == '{' && seg[len(seg)-1] == '}' {
		return seg[1 : len(seg)-1], true
	}
	return "", false
}

// ServicePrefix returns the `@prefix("...")` string declared on the
// service decl, or "" when absent.
func ServicePrefix(svc *ast.ServiceDecl) string {
	if svc == nil {
		return ""
	}
	for _, d := range svc.Decorators {
		if d.Name != "prefix" || len(d.Args) == 0 {
			continue
		}
		if s, ok := d.Args[0].Value.(*ast.StringLit); ok {
			return s.Value
		}
	}
	return ""
}

// ServiceGroup returns the cleaned `@group("a/b")` path declared on a service
// or `extend service` block, or "" when the decorator is absent. @group is the
// URL's mirror image: it decides where a block's GENERATED FILES land on disk
// and never contributes a path segment to the route (see [Resolve]). Values are
// normalised through [CleanGroupPath]; the analyser rejects traversal and
// non-plain segments outright before codegen or the collision check read them.
func ServiceGroup(svc *ast.ServiceDecl) string {
	if svc == nil {
		return ""
	}
	for _, d := range svc.Decorators {
		if d == nil || d.Name != "group" || len(d.Args) == 0 {
			continue
		}
		if s, ok := d.Args[0].Value.(*ast.StringLit); ok {
			return CleanGroupPath(s.Value)
		}
	}
	return ""
}

// EffectiveGroup returns the @group that applies to one service block. The
// block's own @group wins; an extend block declaring none inherits the primary
// block's, so `@group("admin")` on the service covers its extend blocks unless
// an extend overrides it. Pass the primary block's own group as primaryGroup
// (it is its own effective group).
func EffectiveGroup(block *ast.ServiceDecl, primaryGroup string) string {
	if g := ServiceGroup(block); g != "" {
		return g
	}
	return primaryGroup
}

// CleanGroupPath normalises a @group value into a relative slash path: it trims
// surrounding slashes and drops empty segments so "/admin/" and "admin//ops"
// become "admin" and "admin/ops". Traversal (".", "..") segments are dropped as
// a defence-in-depth backstop - the semantic phase rejects them outright - so a
// malformed value reaching codegen can only ever nest deeper inside the output
// tree, never escape it.
func CleanGroupPath(raw string) string {
	segs := strings.Split(raw, "/")
	kept := segs[:0]
	for _, s := range segs {
		if s == "" || s == "." || s == ".." {
			continue
		}
		kept = append(kept, s)
	}
	return strings.Join(kept, "/")
}

// OutputSegment returns the path segment, under any output base, that holds a
// service block's generated files. A non-empty @group REPLACES the service-name
// segment entirely (so `@group("v2")` on any service emits to `<base>/v2/`),
// giving the author full control of the layout; the ungrouped case falls back
// to the service directory under the configured file case. The result is a
// forward-slash path - the group may itself be nested ("admin/ops").
//
// Because the group replaces the service name it is effectively a GLOBAL
// namespace: two services picking the same group would land in one directory
// and overwrite each other's routes file. This is the segment the analyser's
// group-collision check compares, so the directory the editor diagnoses is the
// directory codegen writes.
func OutputSegment(svcName, group, fileCase string) string {
	if group != "" {
		return group
	}
	return idents.FileName(svcName, fileCase)
}

// PatternsConflict reports whether two same-verb mux patterns overlap with
// neither strictly more specific - the exact condition net/http rejects. It
// models craftgo's single-segment wildcards (`{name}`): patterns of different
// segment counts can never overlap, and at each shared position a literal beats
// a wildcard. The pair conflicts when one is more specific at some segment AND
// the other is more specific at another (a cross-over), or when they are the
// same pattern (every segment ties) - i.e. neither side wins outright.
func PatternsConflict(a, b string) bool {
	as, bs := splitRouteSegments(a), splitRouteSegments(b)
	if len(as) != len(bs) {
		return false
	}
	aMoreSpecific, bMoreSpecific := false, false
	for i := range as {
		_, aWild := varName(as[i])
		_, bWild := varName(bs[i])
		switch {
		case !aWild && !bWild:
			if as[i] != bs[i] {
				return false // disjoint at this literal segment
			}
		case !aWild && bWild:
			aMoreSpecific = true
		case aWild && !bWild:
			bMoreSpecific = true
			// both wildcard → tie, no winner at this segment
		}
	}
	// Overlapping (no disjoint segment). Conflict unless exactly one side is
	// strictly more specific overall.
	return aMoreSpecific == bMoreSpecific
}

func splitRouteSegments(pattern string) []string {
	var out []string
	for s := range strings.SplitSeq(pattern, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
