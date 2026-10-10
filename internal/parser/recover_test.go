package parser

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// outline renders the declarations of f with their members: a type's fields,
// an enum's values, a service's methods as `verb Name`.
func outline(f *ast.File) []string {
	var out []string
	for _, d := range f.Decls {
		var members []string
		switch v := d.(type) {
		case *ast.TypeDecl:
			for _, fl := range ast.Fields(v.Body) {
				members = append(members, fl.Name)
			}
		case *ast.EnumDecl:
			for _, ev := range v.EnumValues() {
				members = append(members, ev.Name)
			}
		case *ast.ServiceDecl:
			for _, m := range v.Methods() {
				members = append(members, m.Verb+" "+m.Name)
			}
		}
		out = append(out, d.DeclName()+"{"+strings.Join(members, ",")+"}")
	}
	return out
}

// One mistake is one diagnostic: the parser resumes at the next declaration,
// member or line, and reports an unclosed bracket at its opener.
func TestOneMistakeOneDiagnostic(t *testing.T) {
	for _, c := range []struct {
		name, src string
		want      string
		outline   []string
	}{
		{
			"misspelt keyword",
			"tpye A {\n\ta string\n\tb int\n\tc bool\n}\n\ntype B { b string }\n",
			"3:1: unknown keyword `tpye` - did you mean `type`?",
			[]string{"B{b}"},
		},
		{
			"unknown word",
			"Foo bar\n\ntype B { b string }\n",
			"3:1: unknown keyword `Foo` - a declaration starts with type, enum, error, scalar, service, extend, middleware, event",
			[]string{"B{b}"},
		},
		{
			"upper-case verb",
			"service S {\n\tGET A /a {}\n\tpost B /b {}\n}\n",
			"4:2: HTTP verbs are lower case - write `get`",
			[]string{"S{get A,post B}"},
		},
		{
			"unclosed decorator arguments",
			"type A {\n\ta string @length(1, 5\n\tb string\n\tc string\n}\n",
			"4:18: unclosed `(` - add the `)` that closes it",
			[]string{"A{a,b,c}"},
		},
		{
			"consume in a service",
			"service S {\n\tconsume SendReceipt {\n\t\tpayload X\n\t}\n\tget A /a {}\n}\n",
			"4:2: a service has no `consume` member - which events a deployable listens to is Go code, written where its bus is built",
			[]string{"S{get A}"},
		},
		{
			"verb outside a service",
			"get A /a {}\n\ntype B { b string }\n",
			"3:1: expected declaration, got get",
			[]string{"B{b}"},
		},
		{
			"stray closing brace",
			"type A { a string }\n}\ntype B { b string }\n",
			"4:1: expected declaration, got }",
			[]string{"A{a}", "B{b}"},
		},
		{
			"junk in a type body",
			"type A {\n\ta string\n\t= 5 @x\n\tb string\n}\n",
			"5:2: expected field or mixin, got =",
			[]string{"A{a,b}"},
		},
		{
			"junk in an enum body",
			"enum E {\n\tA\n\t, 5\n\tB\n}\n",
			"5:2: expected enum value name, got ,",
			[]string{"E{A,B}"},
		},
		{
			"junk in a method body",
			"service S {\n\tget A /a {\n\t\treturns X\n\t\tresponse R\n\t}\n\tget B /b {}\n}\n",
			"5:3: expected request or response in method body, got Ident",
			[]string{"S{get A,get B}"},
		},
		{
			"type body left open",
			"type A {\n\ta string\n\ntype B {\n\tb int\n}\n",
			"3:8: unclosed `{` - add the `}` that closes it",
			[]string{"A{a}", "B{b}"},
		},
		{
			"method body left open",
			"service S {\n\tget A /a {\n\t\trequest R\n\n\tget B /b {}\n}\n",
			"4:11: unclosed `{` - add the `}` that closes it",
			[]string{"S{get A,get B}"},
		},
		{
			"body left open to the end",
			"type A {\n\ta string\n",
			"3:8: unclosed `{` - add the `}` that closes it",
			[]string{"A{a}"},
		},
		{
			"extend without service",
			"extend Foo {\n\tget A /a {}\n}\n\ntype B { b string }\n",
			"3:8: expected 'service' after 'extend'",
			[]string{"B{b}"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := New("t.craftgo", "package p\n\n"+c.src)
			f := p.Parse()
			var got []string
			for _, d := range p.Diagnostics() {
				got = append(got, fmt.Sprintf("%d:%d: %s", d.Pos.Line, d.Pos.Column, d.Msg))
			}
			if len(got) != 1 || got[0] != c.want {
				t.Errorf("diagnostics = %q, want [%s]", got, c.want)
			}
			if o := outline(f); !slices.Equal(o, c.outline) {
				t.Errorf("declarations = %q, want %q", o, c.outline)
			}
		})
	}
}
