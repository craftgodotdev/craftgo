package golang

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// flattenFieldsWithNames is [semantic.FlattenFields] with the Go field
// names this package renders each level with.
func flattenFieldsWithNames(td *ast.TypeDecl, prefix string, r *projectResolver) []semantic.FlatField {
	return semantic.FlattenFields(td, prefix, r.Resolver, resolvedGoFieldNames)
}
