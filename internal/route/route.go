// Package route is the leaf authority for craftgo's HTTP routes: how a
// method's final route is assembled (base path + @prefix + method path),
// the string form of a DSL path, the shape key two colliding routes share,
// and net/http's pattern-overlap rule. The analyzer, the routes/OpenAPI
// emitters, and the route-conflict detector all read these - one
// implementation, so the route the editor diagnoses is byte-for-byte the
// route the generated server mounts.
package route

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
)

// ResolveRoute joins the OpenAPI base path, the service's @prefix, and the
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

// Shape strips parameter names from a resolved route string,
// replacing every `{name}` segment with `{}`. Mirrors PathShape but
// operates on the already-joined route (post-prefix, post-basePath)
// that resolveMethodPath produces.
func Shape(route string) string {
	var sb strings.Builder
	sb.Grow(len(route))
	i := 0
	for i < len(route) {
		if route[i] == '{' {
			end := strings.IndexByte(route[i:], '}')
			if end < 0 {
				sb.WriteString(route[i:])
				break
			}
			sb.WriteString("{}")
			i += end + 1
			continue
		}
		sb.WriteByte(route[i])
		i++
	}
	return sb.String()
}

// decoratorString returns the first string-literal positional arg of
// `@name(...)` on the service decl, or "" when absent. Used to read
// `@prefix` and `@group` without depending on codegen helpers.
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

// patternsConflict reports whether two same-verb mux patterns overlap with
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
		aWild, bWild := isWildcardSeg(as[i]), isWildcardSeg(bs[i])
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

func isWildcardSeg(seg string) bool {
	return strings.HasPrefix(seg, "{")
}
