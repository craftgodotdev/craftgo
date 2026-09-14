// Cross-cutting decorator reads used by the transport, types and OpenAPI
// emitters: HTTP status resolution, binding kind, and the description
// fallback from `@doc` to the leading comment block.
package golang

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
)

// hasNullableDecorator reports whether `@nullable` appears on the
// field. The DSL already has `T?` for "optional" (field can be absent);
// `@nullable` is the orthogonal "value can be null when present" flag,
// surfaced via OpenAPI's null-type entry so spec consumers know
// `null` is a valid wire value.
func hasNullableDecorator(ds []*ast.Decorator) bool { return ast.HasDecorator(ds, "nullable") }
