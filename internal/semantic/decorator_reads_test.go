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
