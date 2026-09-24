package golang

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
)

func hasNullableDecorator(ds []*ast.Decorator) bool { return ast.HasDecorator(ds, "nullable") }
