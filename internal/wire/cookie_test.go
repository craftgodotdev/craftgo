package wire

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// A @cookie's attributes are the ones it names over the safe defaults; its name and an
// attribute of the wrong kind are left alone.
func TestCookieAttrsOf(t *testing.T) {
	named := func(name string, v ast.Expr) *ast.DecoratorArg {
		return &ast.DecoratorArg{Name: name, Named: true, Value: v}
	}
	f := &ast.Field{Name: "token", Decorators: []*ast.Decorator{{Name: BindingCookie, Args: []*ast.DecoratorArg{
		{Value: &ast.StringLit{Value: "/not-a-path"}},
		named(CookieMaxAge, &ast.DurationLit{Text: "2h"}),
		named(CookieSecure, &ast.BoolLit{Value: false}),
		named(CookieSameSite, &ast.IdentExpr{Name: &ast.QualifiedIdent{Parts: []string{SameSiteStrict}}}),
		named(CookieHTTPOnly, &ast.StringLit{Value: "no"}),
	}}}}
	want := CookieAttrs{Path: "/", MaxAge: 7200, HTTPOnly: true, SameSite: SameSiteStrict}
	if got := CookieAttrsOf(f); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if got := CookieAttrsOf(&ast.Field{Name: "plain"}); got != DefaultCookieAttrs {
		t.Errorf("no @cookie: got %+v, want the defaults", got)
	}
}
