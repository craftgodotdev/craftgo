package ast

import (
	"reflect"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/lexer"
)

func dec(name string, args ...Expr) *Decorator {
	d := &Decorator{Name: name}
	for _, a := range args {
		d.Args = append(d.Args, &DecoratorArg{Value: a})
	}
	return d
}

func str(v string) Expr { return &StringLit{Value: v} }

func ident(parts ...string) Expr { return &IdentExpr{Name: &QualifiedIdent{Parts: parts}} }

func TestStringArg(t *testing.T) {
	cases := []struct {
		name     string
		decs     []*Decorator
		lookup   string
		wantText string
		wantOK   bool
	}{
		{"absent", nil, "doc", "", false},
		{"other decorator only", []*Decorator{dec("summary", str("s"))}, "doc", "", false},
		{"present with text", []*Decorator{dec("doc", str("hello"))}, "doc", "hello", true},
		{"present but empty is not absent", []*Decorator{dec("doc", str(""))}, "doc", "", true},
		{"flag form has no argument", []*Decorator{dec("deprecated")}, "deprecated", "", false},
		{"non-string argument is skipped", []*Decorator{dec("doc", &IntLit{})}, "doc", "", false},
		{"nil entry does not panic", []*Decorator{nil, dec("doc", str("x"))}, "doc", "x", true},
		{"array shortcut is flattened", []*Decorator{dec("doc", &ArrayLit{Elements: []Expr{str("inner")}})}, "doc", "inner", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := StringArg(c.decs, c.lookup)
			if got != c.wantText || ok != c.wantOK {
				t.Errorf("StringArg = (%q, %v), want (%q, %v)", got, ok, c.wantText, c.wantOK)
			}
		})
	}
}

func TestArgReadsTheFirstOfItsType(t *testing.T) {
	decs := []*Decorator{dec("doc", str("x")), dec("status", str("201"), &IntLit{Value: 201})}
	if got, ok := Arg[*IntLit](decs, "status"); !ok || got.Value != 201 {
		t.Errorf("Arg[*IntLit] = (%v, %v), want 201", got, ok)
	}
	if got, ok := Arg[*IntLit](decs, "doc"); ok {
		t.Errorf("Arg[*IntLit] on @doc = %v, want none", got)
	}
}

func TestArgNames(t *testing.T) {
	at := func(col int) Pos { return lexer.Position{Line: 1, Column: col} }
	d := &Decorator{Name: "errors", Args: []*DecoratorArg{
		{Value: &IdentExpr{Pos: at(9), Name: &QualifiedIdent{Parts: []string{"shared", "Gone"}}}},
		{Value: &ArrayLit{Elements: []Expr{
			&StringLit{Pos: at(22), Value: "b"},
			&IntLit{Pos: at(27), Value: 1},
		}}},
		{Named: true, Name: "k", Value: str("named")},
		{Value: &IntLit{Value: 2}},
		nil,
	}}
	want := []ArgName{{Value: "shared.Gone", Pos: at(9)}, {Value: "b", Pos: at(22)}}
	if got := ArgNames(d); !reflect.DeepEqual(got, want) {
		t.Errorf("ArgNames = %+v, want %+v", got, want)
	}
	if got := ArgNames(dec("errors")); got != nil {
		t.Errorf("ArgNames without arguments = %+v, want nil", got)
	}
}

func TestTextValue(t *testing.T) {
	cases := []struct {
		name   string
		e      Expr
		want   string
		wantOK bool
	}{
		{"ident", ident("x"), "x", true},
		{"qualified ident", ident("shared", "X"), "shared.X", true},
		{"ident without a name", &IdentExpr{}, "", false},
		{"string", str("y"), "y", true},
		{"int", &IntLit{}, "", false},
		{"nil", nil, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := TextValue(c.e)
			if got != c.want || ok != c.wantOK {
				t.Errorf("TextValue = (%q, %v), want (%q, %v)", got, ok, c.want, c.wantOK)
			}
		})
	}
}
