package docs

import (
	"math/big"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/strfmt"
	"github.com/getkin/kin-openapi/openapi3"
)

// rawSchemaDescription describes a `@format(raw)` schema, which has no type.
const rawSchemaDescription = "raw encoded value"

// schemaKeyword stamps one constraint decorator onto a schema.
type schemaKeyword func(d *ast.Decorator, s *openapi3.Schema)

// schemaKeywords maps each constraint decorator of [semantic.Names] with a
// schema form to its keyword; a ConstraintRuntime decorator has no row.
var schemaKeywords = map[string]schemaKeyword{
	"length": func(d *ast.Decorator, s *openapi3.Schema) {
		if !lengthKeywordsApply(s) {
			return
		}
		// `@length(N)` is an exact length, `@length(min, max)` a range.
		lo, ok := countArg(d, 0)
		if !ok {
			return
		}
		hi := lo
		if v, ok := countArg(d, 1); ok {
			hi = v
		}
		setMinLen(s, lo)
		setMaxLen(s, hi)
	},
	"minLength": func(d *ast.Decorator, s *openapi3.Schema) {
		if v, ok := countArg(d, 0); ok && lengthKeywordsApply(s) {
			setMinLen(s, v)
		}
	},
	"maxLength": func(d *ast.Decorator, s *openapi3.Schema) {
		if v, ok := countArg(d, 0); ok && lengthKeywordsApply(s) {
			setMaxLen(s, v)
		}
	},
	"pattern": func(d *ast.Decorator, s *openapi3.Schema) {
		if len(d.Args) == 1 {
			if sl, ok := d.Args[0].Value.(*ast.StringLit); ok {
				s.Pattern = sl.Value
			}
		}
	},
	"format": func(d *ast.Decorator, s *openapi3.Schema) {
		if len(d.Args) != 1 {
			return
		}
		name, _ := ast.TextValue(d.Args[0].Value)
		if name == "" {
			return
		}
		if name == semantic.FormatRaw {
			// Raw bytes travel in the message's own encoding, not base64.
			s.Type, s.Format = nil, ""
			s.Description = appendDescription(s.Description, rawSchemaDescription)
			return
		}
		s.Format = strfmt.OpenAPIFormat(name)
	},
	"gt":  func(d *ast.Decorator, s *openapi3.Schema) { emitBound(s, "exclusiveMinimum", d, 0) },
	"gte": func(d *ast.Decorator, s *openapi3.Schema) { emitBound(s, "minimum", d, 0) },
	"lt":  func(d *ast.Decorator, s *openapi3.Schema) { emitBound(s, "exclusiveMaximum", d, 0) },
	"lte": func(d *ast.Decorator, s *openapi3.Schema) { emitBound(s, "maximum", d, 0) },
	"range": func(d *ast.Decorator, s *openapi3.Schema) {
		emitBound(s, "minimum", d, 0)
		emitBound(s, "maximum", d, 1)
	},
	"positive": func(_ *ast.Decorator, s *openapi3.Schema) { tightenBound(s, "exclusiveMinimum", new(big.Rat)) },
	"negative": func(_ *ast.Decorator, s *openapi3.Schema) { tightenBound(s, "exclusiveMaximum", new(big.Rat)) },
	"multipleOf": func(d *ast.Decorator, s *openapi3.Schema) {
		if v, ok := numberArg(d, 0); ok && v.Sign() != 0 {
			setNumber(s, "multipleOf", v)
		}
	},
	"minItems": func(d *ast.Decorator, s *openapi3.Schema) {
		itemCountKeyword(s, d, func(u uint64) { s.MinItems = u }, func(u uint64) { s.MinProps = u })
	},
	"maxItems": func(d *ast.Decorator, s *openapi3.Schema) {
		itemCountKeyword(s, d, func(u uint64) { s.MaxItems = &u }, func(u uint64) { s.MaxProps = &u })
	},
	"uniqueItems": func(_ *ast.Decorator, s *openapi3.Schema) {
		if s.Type != nil && s.Type.Includes("array") {
			s.UniqueItems = true
		}
	},
}

// applyFieldConstraints stamps the keyword of every constraint in ds that
// has a schema form onto s.
func applyFieldConstraints(ds []*ast.Decorator, s *openapi3.Schema) {
	applyConstraintFamilies(ds, s, semantic.ConstraintSchema)
}

// applyConstraintFamilies stamps onto s the keywords of the decorators in
// ds whose family is in fams.
func applyConstraintFamilies(ds []*ast.Decorator, s *openapi3.Schema, fams semantic.ConstraintFamily) {
	if s == nil {
		return
	}
	for _, d := range ds {
		if d == nil {
			continue
		}
		if kw := schemaKeywords[d.Name]; kw != nil && semantic.ConstraintOf(d.Name)&fams != 0 {
			kw(d, s)
		}
	}
}

// hasFieldConstraintDecorator reports whether ds carries a decorator that
// narrows a referenced type with an OpenAPI validation keyword.
func hasFieldConstraintDecorator(ds []*ast.Decorator) bool {
	for _, d := range ds {
		if d == nil {
			continue
		}
		if schemaKeywords[d.Name] != nil && semantic.ConstraintOf(d.Name)&semantic.ConstraintNarrowing != 0 {
			return true
		}
	}
	return false
}
