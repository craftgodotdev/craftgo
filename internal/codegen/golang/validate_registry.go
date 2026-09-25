package golang

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
)

// goCheck renders the Go source for one constraint check, or "" to opt
// out when the field type does not fit.
type goCheck func(f *ast.Field, access string, d *ast.Decorator, ctx emitCtx) string

// goChecks maps every constraint decorator of [semantic.Names] to its Go check.
var goChecks = map[string]goCheck{
	// string
	"length": lengthCheck,
	"minLength": func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return minMaxLengthCheck(f, a, d, "min", c)
	},
	"maxLength": func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return minMaxLengthCheck(f, a, d, "max", c)
	},
	"pattern": patternCheck,
	"format":  formatCheck,
	// numeric
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
	"range": rangeCheck,
	"positive": func(f *ast.Field, a string, _ *ast.Decorator, c emitCtx) string {
		return signCheck(f, a, "positive", c)
	},
	"negative": func(f *ast.Field, a string, _ *ast.Decorator, c emitCtx) string {
		return signCheck(f, a, "negative", c)
	},
	"multipleOf": multipleOfCheck,
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
	// file
	"maxSize":   maxSizeCheck,
	"mimeTypes": mimeTypesCheck,
}
