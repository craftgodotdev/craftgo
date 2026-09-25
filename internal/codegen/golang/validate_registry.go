package golang

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
)

// goCheck renders the Go source for one constraint check on t, or "" to opt
// out when t's type does not fit.
type goCheck func(t checkTarget, d *ast.Decorator, ctx emitCtx) string

// goChecks maps every constraint decorator of [semantic.Names] to its Go check.
var goChecks = map[string]goCheck{
	// string
	"length": lengthCheck,
	"minLength": func(t checkTarget, d *ast.Decorator, c emitCtx) string {
		return minMaxLengthCheck(t, d, "min", c)
	},
	"maxLength": func(t checkTarget, d *ast.Decorator, c emitCtx) string {
		return minMaxLengthCheck(t, d, "max", c)
	},
	"pattern": patternCheck,
	"format":  formatCheck,
	// numeric
	"gt": func(t checkTarget, d *ast.Decorator, c emitCtx) string {
		return numericBoundCheck(t, d, ">", "must be greater than", c)
	},
	"gte": func(t checkTarget, d *ast.Decorator, c emitCtx) string {
		return numericBoundCheck(t, d, ">=", "below minimum", c)
	},
	"lt": func(t checkTarget, d *ast.Decorator, c emitCtx) string {
		return numericBoundCheck(t, d, "<", "must be less than", c)
	},
	"lte": func(t checkTarget, d *ast.Decorator, c emitCtx) string {
		return numericBoundCheck(t, d, "<=", "above maximum", c)
	},
	"range": rangeCheck,
	"positive": func(t checkTarget, _ *ast.Decorator, c emitCtx) string {
		return signCheck(t, "positive", c)
	},
	"negative": func(t checkTarget, _ *ast.Decorator, c emitCtx) string {
		return signCheck(t, "negative", c)
	},
	"multipleOf": multipleOfCheck,
	// array
	"minItems": func(t checkTarget, d *ast.Decorator, c emitCtx) string {
		return itemsBoundCheck(t, d, ">=", "minItems", c)
	},
	"maxItems": func(t checkTarget, d *ast.Decorator, c emitCtx) string {
		return itemsBoundCheck(t, d, "<=", "maxItems", c)
	},
	"uniqueItems": func(t checkTarget, _ *ast.Decorator, c emitCtx) string {
		return uniqueItemsCheck(t, c)
	},
	// file
	"maxSize":   maxSizeCheck,
	"mimeTypes": mimeTypesCheck,
}
