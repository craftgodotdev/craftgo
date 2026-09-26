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
	"length":    lengthCheck,
	"minLength": minMaxLengthCheck,
	"maxLength": minMaxLengthCheck,
	"pattern":   patternCheck,
	"format":    formatCheck,
	// numeric
	"gt":         numericBoundCheck,
	"gte":        numericBoundCheck,
	"lt":         numericBoundCheck,
	"lte":        numericBoundCheck,
	"range":      rangeCheck,
	"positive":   signCheck,
	"negative":   signCheck,
	"multipleOf": multipleOfCheck,
	// array
	"minItems": itemsBoundCheck,
	"maxItems": itemsBoundCheck,
	"uniqueItems": func(t checkTarget, _ *ast.Decorator, c emitCtx) string {
		return uniqueItemsCheck(t, c)
	},
	// file
	"maxSize":   maxSizeCheck,
	"mimeTypes": mimeTypesCheck,
}
