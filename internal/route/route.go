// Package route builds a method's URL route, finds the segments and pattern
// pairs net/http's ServeMux refuses, and names the directory of a service
// block's generated files. `@group` shapes only that directory, never the URL.
package route

import (
	"strings"
	"unicode"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
)

// Resolve joins basePath, the service's @prefix and the method's path into an
// absolute route with no empty segments or trailing slash. A pathless method
// uses its kebab-cased name: "Ping" → "/ping".
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

// ServePath is the path of m's ServeMux pattern: its [Resolve] route, or for the root route, a
// whole route of "/" under basePath, the basePath with `/{$}`, which matches that one path where a
// pattern ending in "/" would match every path below it, and which the documented `/` reaches.
func ServePath(basePath string, svc *ast.ServiceDecl, m *ast.Method) string {
	full := Resolve(basePath, svc, m)
	if Resolve("", svc, m) != "/" {
		return full
	}
	return strings.TrimSuffix(full, "/") + "/{$}"
}

// PathString renders a DSL path in route form, e.g. `/users/{id}`; "" for nil.
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

// Shape replaces the name of every variable segment of a route, `{}` for
// `{name}` and `{...}` for `{name...}`, so routes that differ only in variable
// names compare equal; `{$}` stays.
func Shape(route string) string {
	segs := Segments(route)
	for i, seg := range segs {
		switch {
		case seg == "{$}":
		case isVarSegment(seg) && strings.HasSuffix(seg, "...}"):
			segs[i] = "{...}"
		case isVarSegment(seg):
			segs[i] = "{}"
		}
	}
	return "/" + strings.Join(segs, "/")
}

// Vars returns the names of the variables of a route, prefix or path, in
// order: `name` for `{name}` and `{name...}`, as net/http's PathValue reads
// them.
func Vars(route string) []string {
	var out []string
	for _, seg := range Segments(route) {
		if name, ok := WildcardName(seg); ok {
			out = append(out, name)
		}
	}
	return out
}

// OpenAPIPath spells route as an OpenAPI path template: `{name...}` as
// `{name}`, and `{$}` as the trailing slash it matches.
func OpenAPIPath(route string) string {
	segs := Segments(route)
	for i, seg := range segs {
		if seg == "{$}" {
			segs[i] = ""
		} else if name, ok := WildcardName(seg); ok {
			segs[i] = "{" + name + "}"
		}
	}
	return "/" + strings.Join(segs, "/")
}

// isVarSegment reports whether seg is a whole-segment `{...}` variable,
// `{name...}` and `{$}` included.
func isVarSegment(seg string) bool {
	return len(seg) > 2 && seg[0] == '{' && seg[len(seg)-1] == '}'
}

// ServicePrefix returns the service's `@prefix("...")` string, or "".
func ServicePrefix(svc *ast.ServiceDecl) string {
	if svc == nil {
		return ""
	}
	prefix, _ := ast.StringArg(svc.Decorators, "prefix")
	return prefix
}

// ServiceGroup returns the `@group("a/b")` path of a service or extend block,
// cleaned by [CleanGroupPath], or "" when absent.
func ServiceGroup(svc *ast.ServiceDecl) string {
	if svc == nil {
		return ""
	}
	group, _ := ast.StringArg(svc.Decorators, "group")
	return CleanGroupPath(group)
}

// EffectiveGroup returns the @group of a service block: its own, else the
// primary block's group, passed as primaryGroup.
func EffectiveGroup(block *ast.ServiceDecl, primaryGroup string) string {
	if g := ServiceGroup(block); g != "" {
		return g
	}
	return primaryGroup
}

// CleanGroupPath normalises a @group value to a relative slash path, dropping
// empty, "." and ".." segments so the result stays inside the output tree.
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

// OutputSegment returns the slash path, under an output base, of a service
// block's generated files: the group when set, else the service name in
// fileCase. Services sharing a group therefore share a directory.
func OutputSegment(svcName, group, fileCase string) string {
	if group != "" {
		return group
	}
	return idents.FileName(svcName, fileCase)
}

// relation is how two routes, or two of their segments, compare as
// net/http's ServeMux ranks them.
type relation uint8

const (
	equivalent   relation = iota // both match the same paths
	moreGeneral                  // the first matches every path the second does, and more
	moreSpecific                 // the second matches every path the first does, and more
	disjoint                     // no path matches both
	overlaps                     // some path matches both, and neither is more specific
)

// combine returns the relation of two routes whose parts relate as r and s.
func combine(r, s relation) relation {
	switch {
	case r == equivalent:
		return s
	case r == disjoint || s == disjoint:
		return disjoint
	case r == overlaps || s == overlaps:
		return overlaps
	case s == equivalent || s == r:
		return r
	}
	return overlaps // one side more general, the other more specific
}

// compareSegments relates two segments of routes as ServeMux does: a
// `{name...}` matches the rest of a path and is more general than any other
// segment, `{name}` one segment, a literal only itself, and `{$}` only the
// end of a path after its `/`.
func compareSegments(a, b string) relation {
	aMulti, bMulti := EndsRoute(a) && a != "{$}", EndsRoute(b) && b != "{$}"
	aWild, bWild := isVarSegment(a) && !EndsRoute(a), isVarSegment(b) && !EndsRoute(b)
	switch {
	case aMulti && bMulti:
		return equivalent
	case aMulti:
		return moreGeneral
	case bMulti:
		return moreSpecific
	case aWild && bWild:
		return equivalent
	case aWild:
		if b == "{$}" {
			return disjoint
		}
		return moreGeneral
	case bWild:
		if a == "{$}" {
			return disjoint
		}
		return moreSpecific
	case a == b:
		return equivalent
	}
	return disjoint
}

// PatternsConflict reports whether two same-verb mux patterns with as many
// segments match a common path with neither more specific, which net/http
// rejects. Routes of different lengths are never reported.
func PatternsConflict(a, b string) bool {
	as, bs := Segments(a), Segments(b)
	if len(as) != len(bs) {
		return false
	}
	rel := equivalent
	for i := range as {
		if rel = combine(rel, compareSegments(as[i], bs[i])); rel == disjoint {
			return false
		}
	}
	return rel == equivalent || rel == overlaps
}

// Segments returns the non-empty segments of a route, prefix or path.
func Segments(pattern string) []string {
	var out []string
	for s := range strings.SplitSeq(pattern, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// SegmentProblem says why net/http's ServeMux refuses seg, one segment of a
// route, wherever it stands, or returns "": a `.` or `..`, or a `{` that
// opens no whole-segment `{name}`, `{name...}` or `{$}` whose name is a Go
// identifier.
func SegmentProblem(seg string) string {
	if seg == "." || seg == ".." {
		return "net/http cleans `.` and `..` out of a request path before matching, so no request would match"
	}
	if !strings.Contains(seg, "{") {
		return ""
	}
	if seg[0] != '{' || seg[len(seg)-1] != '}' {
		return "a path variable is a whole segment, `{name}`"
	}
	inner := seg[1 : len(seg)-1]
	if inner == "$" {
		return ""
	}
	switch name := strings.TrimSuffix(inner, "..."); {
	case name == "":
		return "a path variable needs a name"
	case !isGoIdent(name):
		return "a path variable's name is a Go identifier"
	}
	return ""
}

// EndsRoute reports whether seg is a wildcard net/http's ServeMux takes only
// as the last segment of a route: `{name...}` or `{$}`.
func EndsRoute(seg string) bool {
	return len(seg) > 2 && seg[0] == '{' && (seg == "{$}" || strings.HasSuffix(seg, "...}"))
}

// WildcardName returns the name net/http's ServeMux gives wildcard segment
// seg, `{name}` or `{name...}`; ok is false for any other segment, `{$}`
// included.
func WildcardName(seg string) (string, bool) {
	if len(seg) < 3 || seg[0] != '{' || seg[len(seg)-1] != '}' || seg == "{$}" {
		return "", false
	}
	name := strings.TrimSuffix(seg[1:len(seg)-1], "...")
	return name, name != ""
}

// isGoIdent reports whether s is a Go identifier: a letter or `_`, then
// letters, digits and `_`.
func isGoIdent(s string) bool {
	for i, r := range s {
		if !unicode.IsLetter(r) && r != '_' && (i == 0 || !unicode.IsDigit(r)) {
			return false
		}
	}
	return s != ""
}
