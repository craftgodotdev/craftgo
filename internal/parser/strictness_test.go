package parser

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
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

// A token in place of a name is reported and never becomes the name: the
// declaration is left nameless.
func TestMismatchedTokenIsNoName(t *testing.T) {
	for _, src := range []string{
		"error {\n}\n",
		"type { a string }\n",
		"enum { A }\n",
		"service {\n}\n",
		"middleware (\n",
		"event { payload T }\n",
		"scalar Email {\n",
	} {
		f, msgs := parseWithErrors(t, "package p\n\n"+src)
		if len(msgs) == 0 {
			t.Errorf("%q: no diagnostic", src)
		}
		for _, m := range msgs {
			if strings.Contains(m, `"{"`) || strings.Contains(m, `"("`) {
				t.Errorf("%q: diagnostic names the token: %s", src, m)
			}
		}
		for _, d := range f.Decls {
			if name := d.DeclName(); name == "{" || name == "(" {
				t.Errorf("%q: declaration named %q", src, name)
			}
			if sd, ok := d.(*ast.ScalarDecl); ok && sd.Primitive != "" {
				t.Errorf("%q: scalar primitive %q", src, sd.Primitive)
			}
		}
	}
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

// A decorator on the line where a declaration or a method ends is reported,
// and the declaration or method below takes no decorator.
func TestDecoratorAfterADeclarationOnItsLine(t *testing.T) {
	for name, src := range map[string]string{
		"middleware":      "package p\n\nmiddleware M @doc(\"x\")\n\ntype T {\n\tid string\n}\n",
		"bodiless error":  "package p\n\nerror NotFound E @doc(\"x\")\n\ntype T {\n\tid string\n}\n",
		"closing brace":   "package p\n\ntype A {\n\tid string\n} @doc(\"x\")\n\ntype T {\n\tid string\n}\n",
		"next middleware": "package p\n\nmiddleware M1 @doc(\"x\")\nmiddleware T\n",
		"package clause":  "package p @doc(\"x\")\n\ntype T {\n\tid string\n}\n",
		"import":          "package p\n\nimport \"a\" @doc(\"x\")\nimport \"b\"\n\ntype T {\n\tid string\n}\n",
		"method":          "package p\n\nservice S {\n\tget A /a {\n\t\tresponse R\n\t} @doc(\"x\")\n\n\tget T /t {\n\t\tresponse R\n\t}\n}\n",
		"last method":     "package p\n\nservice S {\n\tget A /a {} @doc(\"x\")\n}\n\ntype T {\n\tid string\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			f, msgs := parseWithErrors(t, src)
			if len(msgs) != 1 || !strings.Contains(msgs[0], "@doc") {
				t.Errorf("diagnostics = %v, want one naming @doc", msgs)
			}
			last := f.Decls[len(f.Decls)-1]
			var decs []*ast.Decorator
			switch d := last.(type) {
			case *ast.TypeDecl:
				decs = d.Decorators
			case *ast.MiddlewareDecl:
				decs = d.Decorators
			case *ast.ServiceDecl:
				ms := d.Methods()
				decs = ms[len(ms)-1].Decorators
			}
			if len(decs) != 0 {
				t.Errorf("the declaration below took %d decorator(s)", len(decs))
			}
		})
	}
	for _, src := range []string{
		"package p\n\nservice S { @doc(\"a\") get A /a {} }\n",
		"package p\n\n@doc(\"t\") type T { id string } type U { id string }\n",
		"package p\n\nscalar S string @minLength(1)\n@doc(\"t\")\ntype T { id string }\n",
		"package p\n\nenum E { A @doc(\"a\") B }\n",
	} {
		if _, msgs := parseWithErrors(t, src); len(msgs) != 0 {
			t.Errorf("%q: unexpected diagnostics %v", src, msgs)
		}
	}
	for name, src := range map[string]string{
		"import before import": "package p\n\nimport \"a\" @doc(\"x\") import \"b\"\n\ntype T {\n\tid string\n}\n",
		"method before brace":  "package p\n\nservice S {\n\tget A /a {} @doc(\"x\") }\n\ntype T {\n\tid string\n}\n",
	} {
		if _, msgs := parseWithErrors(t, src); len(msgs) != 1 || !strings.Contains(msgs[0], "goes before what it decorates") {
			t.Errorf("%s: diagnostics = %v, want one saying the decorator goes before what it decorates", name, msgs)
		}
	}
}

// A decorator after a declaration or a method on its line decorates the
// declaration or method that starts later on that line.
func TestDecoratorBeforeTheNextDeclarationOnItsLine(t *testing.T) {
	for name, src := range map[string]string{
		"closing brace":  "package p\n\ntype A { a string } @doc(\"b\") type B { b string }\n",
		"middleware":     "package p\n\nmiddleware A @doc(\"b\") middleware B\n",
		"bodiless error": "package p\n\nerror NotFound A @doc(\"b\") type B { b string }\n",
		"package clause": "package p @doc(\"b\") type B { b string }\n",
		"import":         "package p\n\nimport \"a\" @doc(\"b\") type B { b string }\n",
		"extend":         "package p\n\nservice A {} @doc(\"b\") extend service B {}\n",
	} {
		t.Run(name, func(t *testing.T) {
			f, msgs := parseWithErrors(t, src)
			if len(msgs) != 0 {
				t.Fatalf("unexpected diagnostics %v", msgs)
			}
			n := len(f.Decls)
			if n == 2 && len(declDecorators(f.Decls[0])) != 0 {
				t.Errorf("the declaration before took a decorator")
			}
			if last := declDecorators(f.Decls[n-1]); len(last) != 1 || last[0].Name != "doc" {
				t.Errorf("the declaration after has %d decorator(s), want @doc", len(last))
			}
		})
	}
	f, msgs := parseWithErrors(t, "package p\n\ntype R { ok bool }\n\nservice S {\n\tget A /a { response R } @deprecated @doc(\"b\") get B /b { response R }\n}\n")
	if len(msgs) != 0 {
		t.Fatalf("method: unexpected diagnostics %v", msgs)
	}
	ms := f.Decls[1].(*ast.ServiceDecl).Methods()
	if len(ms) != 2 || len(ms[0].Decorators) != 0 || len(ms[1].Decorators) != 2 {
		t.Errorf("method: want A without decorators and B with @deprecated and @doc, got %d methods", len(ms))
	}
}

// startsDecl holds for exactly the reserved words a top-level declaration is
// parsed from.
func TestStartsDeclMatchesTheDeclarations(t *testing.T) {
	for k := lexer.KwPackage; k <= lexer.VerbOptions; k++ {
		_, msgs := parseWithErrors(t, "package p\n\nmiddleware M\n"+k.String()+" X\n")
		parsed := !strings.Contains(strings.Join(msgs, "\n"), "expected declaration, got "+k.String())
		if parsed != startsDecl(k) {
			t.Errorf("%s: startsDecl = %v, but a declaration parsed = %v (%v)", k, startsDecl(k), parsed, msgs)
		}
	}
}

// declDecorators returns the decorators of a type, error, middleware or
// service declaration.
func declDecorators(d ast.Decl) []*ast.Decorator {
	switch d := d.(type) {
	case *ast.TypeDecl:
		return d.Decorators
	case *ast.ErrorDecl:
		return d.Decorators
	case *ast.MiddlewareDecl:
		return d.Decorators
	case *ast.ServiceDecl:
		return d.Decorators
	}
	return nil
}
