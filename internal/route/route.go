// Package route builds a method's URL route, detects net/http pattern
// conflicts, and names the directory of a service block's generated files.
// `@group` shapes only that directory, never the URL.
package route

import (
	"strings"

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
	segs := splitRouteSegments(route)
	for i, seg := range segs {
		if _, ok := varName(seg); ok {
			segs[i] = "{}"
		}
	}
	return "/" + strings.Join(segs, "/")
}

// Vars returns the names of the `{name}` segments of a route, prefix or path,
// in order.
func Vars(route string) []string {
	var out []string
	for _, seg := range splitRouteSegments(route) {
		if name, ok := varName(seg); ok {
			out = append(out, name)
		}
	}
	return out
}

// varName returns the name of a whole-segment `{name}` variable.
func varName(seg string) (string, bool) {
	if len(seg) > 2 && seg[0] == '{' && seg[len(seg)-1] == '}' {
		return seg[1 : len(seg)-1], true
	}
	return "", false
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
	// No disjoint segment: a conflict unless exactly one side is more specific.
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
