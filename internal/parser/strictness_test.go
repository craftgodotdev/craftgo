package parser

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

type (
	astServiceDecl = ast.ServiceDecl
	astMethod      = ast.Method
)

func firstMsg(msgs []string) string {
	if len(msgs) == 0 {
		return ""
	}
	return msgs[0]
}

// TestPathSlashes pins that `//` and a trailing `/` are errors while the root
// path `/` is valid.
func TestPathSlashes(t *testing.T) {
	_, msgs := parseWithErrors(t, "package p\nservice S {\n\tget X /items/ { response A }\n}\n")
	if !strings.Contains(firstMsg(msgs), "path ends with '/'") {
		t.Errorf("trailing slash: diagnostics = %v", msgs)
	}
	_, msgs = parseWithErrors(t, "package p\nservice S {\n\tget X /a/ /b { response A }\n}\n")
	if !strings.Contains(firstMsg(msgs), "empty path segment") {
		t.Errorf("double slash: diagnostics = %v", msgs)
	}
	f, msgs := parseWithErrors(t, "package p\nservice S {\n\tget Root / { response A }\n}\n")
	if len(msgs) != 0 {
		t.Fatalf("root path: unexpected diagnostics %v", msgs)
	}
	if got := pathStr(f.Decls[0].(*astServiceDecl).Members[0].(*astMethod).Path); got != "/" {
		t.Errorf("root path = %q, want /", got)
	}
}

// TestOrphanDecoratorsReported pins that decorators with no declaration after
// them are reported.
func TestOrphanDecoratorsReported(t *testing.T) {
	for _, src := range []string{
		"@doc(\"pkg\")\n@version(\"1\")\n// package p\n",
		"package p\n\ntype A { x string }\n\n@deprecated\n",
		"package p\n\nmiddleware Auth\n@ RequestStamp\n",
	} {
		_, msgs := parseWithErrors(t, src)
		if !strings.Contains(firstMsg(msgs), "decorators without a declaration") {
			t.Errorf("%q: diagnostics = %v", src, msgs)
		}
	}
}

// TestMissingCommaReported pins that a missing list separator is reported.
func TestMissingCommaReported(t *testing.T) {
	cases := map[string]string{
		"package p\ntype A { x string @length(1 2) }\n":          "',' or ')'",
		"package p\ntype A { x string @default([1 2]) }\n":       "',' or ']'",
		"package p\ntype A { x string @default({a: 1 b: 2}) }\n": "',' or '}'",
		"package p\ntype A<T U> { x T }\n":                       "',' or '>'",
		"package p\ntype A { x Box<int string> }\n":              "',' or '>'",
	}
	for src, want := range cases {
		_, msgs := parseWithErrors(t, src)
		if len(msgs) != 1 || !strings.Contains(msgs[0], want) {
			t.Errorf("%q: diagnostics = %v, want one containing %s", src, msgs, want)
		}
	}
	for _, src := range []string{
		"package p\ntype A { x string @length(1, 80) @default([1, 2]) @example({a: 1, b: [2]}) }\n",
		"package p\ntype A<T, U> { x Box<T, U> }\n",
		"package p\ntype A { x string @length(1, ) }\n",
	} {
		if _, msgs := parseWithErrors(t, src); len(msgs) != 0 {
			t.Errorf("%q: unexpected diagnostics %v", src, msgs)
		}
	}
}
