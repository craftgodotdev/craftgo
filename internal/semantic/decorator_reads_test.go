package semantic

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

func dec(name string, args ...ast.Expr) *ast.Decorator {
	d := &ast.Decorator{Name: name}
	for _, a := range args {
		d.Args = append(d.Args, &ast.DecoratorArg{Value: a})
	}
	return d
}

func str(v string) ast.Expr { return &ast.StringLit{Value: v} }

func TestDecoratorStringArg(t *testing.T) {
	cases := []struct {
		name     string
		decs     []*ast.Decorator
		lookup   string
		wantText string
		wantOK   bool
	}{
		{"absent", nil, "doc", "", false},
		{"other decorator only", []*ast.Decorator{dec("summary", str("s"))}, "doc", "", false},
		{"present with text", []*ast.Decorator{dec("doc", str("hello"))}, "doc", "hello", true},
		{"present but empty is not absent", []*ast.Decorator{dec("doc", str(""))}, "doc", "", true},
		{"flag form has no argument", []*ast.Decorator{dec("deprecated")}, "deprecated", "", false},
		{"non-string argument is skipped", []*ast.Decorator{dec("doc", &ast.IntLit{})}, "doc", "", false},
		{"nil entry does not panic", []*ast.Decorator{nil, dec("doc", str("x"))}, "doc", "x", true},
		{"array shortcut is flattened",
			[]*ast.Decorator{{Name: "doc", Args: []*ast.DecoratorArg{
				{Value: &ast.ArrayLit{Elements: []ast.Expr{str("inner")}}}}}}, "doc", "inner", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := DecoratorStringArg(c.decs, c.lookup)
			if got != c.wantText || ok != c.wantOK {
				t.Errorf("DecoratorStringArg = (%q, %v), want (%q, %v)", got, ok, c.wantText, c.wantOK)
			}
		})
	}
}

func TestDeprecatedReads(t *testing.T) {
	cases := []struct {
		name       string
		decs       []*ast.Decorator
		wantMarked bool
		wantReason string
	}{
		{"absent", nil, false, ""},
		{"flag form", []*ast.Decorator{dec("deprecated")}, true, ""},
		{"with reason", []*ast.Decorator{dec("deprecated", str("use Foo"))}, true, "use Foo"},
		{"empty reason", []*ast.Decorator{dec("deprecated", str(""))}, true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsDeprecated(c.decs); got != c.wantMarked {
				t.Errorf("IsDeprecated = %v, want %v", got, c.wantMarked)
			}
			if got := DeprecatedReason(c.decs); got != c.wantReason {
				t.Errorf("DeprecatedReason = %q, want %q", got, c.wantReason)
			}
		})
	}
}
