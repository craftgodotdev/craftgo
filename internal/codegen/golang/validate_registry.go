// The Go half of the constraint catalogue: the runtime check each
// constraint decorator compiles to. Which decorators exist and what
// each restricts is [semantic.Registry]; this file only renders.
package golang

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
)

// goCheck renders the Go source for one constraint check, or "" to opt
// out when the field type does not fit.
type goCheck func(f *ast.Field, access string, d *ast.Decorator, ctx emitCtx) string

// goChecks is keyed by decorator name; every constraint decorator in
// [semantic.Registry] has a row, asserted by TestGoChecksCoverConstraints.
var goChecks = map[string]goCheck{
	// string
	"length": func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string { return lengthCheck(f, a, d, c) },
	"minLength": func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return minMaxLengthCheck(f, a, d, "min", c)
	},
	"maxLength": func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return minMaxLengthCheck(f, a, d, "max", c)
	},
	"pattern": func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string { return patternCheck(f, a, d, c) },
	"format":  func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string { return formatCheck(f, a, d, c) },
	// numeric - math-style comparison operators. Strict variants
	// (@gt, @lt) sit next to inclusive variants (@gte, @lte); no
	// legacy aliases. `@positive`/`@negative` remain as flag-form
	// sugar for `@gt(0)` / `@lt(0)`.
	"gt": func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return numericBoundCheck(f, a, d, ">", "must be greater than", c)
	},
	"gte": func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return numericBoundCheck(f, a, d, ">=", "below minimum", c)
	},
	"lt": func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return numericBoundCheck(f, a, d, "<", "must be less than", c)
	},
	"lte": func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return numericBoundCheck(f, a, d, "<=", "above maximum", c)
	},
	"range": func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string { return rangeCheck(f, a, d, c) },
	"positive": func(f *ast.Field, a string, _ *ast.Decorator, c emitCtx) string {
		return signCheck(f, a, "positive", c)
	},
	"negative": func(f *ast.Field, a string, _ *ast.Decorator, c emitCtx) string {
		return signCheck(f, a, "negative", c)
	},
	"multipleOf": func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return multipleOfCheck(f, a, d, c)
	},
	// array
	"minItems": func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return itemsBoundCheck(f, a, d, ">=", "minItems", c)
	},
	"maxItems": func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return itemsBoundCheck(f, a, d, "<=", "maxItems", c)
	},
	"uniqueItems": func(f *ast.Field, a string, _ *ast.Decorator, c emitCtx) string {
		return uniqueItemsCheck(f, a, c)
	},
	// file - runtime only; a multipart part has no schema keyword for these.
	"maxSize": func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string { return maxSizeCheck(f, a, d, c) },
	"mimeTypes": func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return mimeTypesCheck(f, a, d, c)
	},
}
