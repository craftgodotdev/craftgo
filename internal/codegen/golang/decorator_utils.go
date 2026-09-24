package golang

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
)

func hasNullableDecorator(ds []*ast.Decorator) bool { return ast.HasDecorator(ds, "nullable") }

// firstArg returns the first argument of ds's first @name, or nil.
func firstArg(ds []*ast.Decorator, name string) *ast.DecoratorArg {
	if d := ast.FindDecorator(ds, name); d != nil && len(d.Args) > 0 {
		return d.Args[0]
	}
	return nil
}
