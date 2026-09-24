package golang

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// flattenFieldsWithNames is [semantic.FlattenWithNames] with the Go field
// names this package renders each level with.
func flattenFieldsWithNames(td *ast.TypeDecl, prefix string, pkg *semantic.Package, r *projectResolver, seen map[string]bool) []semantic.FlatField {
	return semantic.FlattenWithNames(td, prefix, pkg, r.Resolver, seen, resolvedGoFieldNames)
}
