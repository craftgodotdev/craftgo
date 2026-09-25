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

// Shape replaces every variable segment of a route with `{}`, so routes that
// differ only in variable names compare equal.
func Shape(route string) string {
	segs := Segments(route)
	for i, seg := range segs {
		if isVarSegment(seg) {
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

// PatternsConflict reports whether two same-verb mux patterns overlap with
// neither more specific, which net/http rejects. A `{name}` wildcard spans one
// segment and a literal is more specific than a wildcard.
func PatternsConflict(a, b string) bool {
	as, bs := Segments(a), Segments(b)
	if len(as) != len(bs) {
		return false
	}
	aMoreSpecific, bMoreSpecific := false, false
	for i := range as {
		aWild, bWild := isVarSegment(as[i]), isVarSegment(bs[i])
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
	// No disjoint segment: a conflict unless exactly one side is more specific.
	return aMoreSpecific == bMoreSpecific
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
