// The OpenAPI half of the constraint catalogue: the schema keyword each
// constraint decorator advertises. Which decorators exist and what each
// restricts is [semantic.Registry]; this file only renders.
package docs

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/strfmt"
	"github.com/getkin/kin-openapi/openapi3"
)

// rawSchemaDescription is what a `bytes @format(raw)` schema says in
// place of a type: the value is carried in the message's own encoding,
// and the document has nothing more to add about it.
const rawSchemaDescription = "raw encoded value"

// schemaKeyword stamps one constraint decorator onto a schema.
type schemaKeyword func(d *ast.Decorator, s *openapi3.Schema)

// schemaKeywords is keyed by decorator name. A constraint with no schema
// form (semantic.ConstraintRuntime) has no row; the pairing is asserted
// by TestSchemaKeywordsCoverConstraints.
var schemaKeywords = map[string]schemaKeyword{
	"length": func(d *ast.Decorator, s *openapi3.Schema) {
		if !lengthKeywordsApply(s) {
			return
		}
		// `@length(N)` is exact length - fold the single argument into
		// both bounds (min == max == N), matching the runtime check and
		// the map-key path; `@length(min, max)` is a range.
		lo, ok := numericArgValue(d, 0)
		if !ok || lo < 0 {
			return
		}
		hi := lo
		if v, ok := numericArgValue(d, 1); ok && v >= 0 {
			hi = v
		}
		setMinLen(s, uint64(lo))
		setMaxLen(s, uint64(hi))
	},
	"minLength": func(d *ast.Decorator, s *openapi3.Schema) {
		if v, ok := numericArgValue(d, 0); ok && v >= 0 && lengthKeywordsApply(s) {
			setMinLen(s, uint64(v))
		}
	},
	"maxLength": func(d *ast.Decorator, s *openapi3.Schema) {
		if v, ok := numericArgValue(d, 0); ok && v >= 0 && lengthKeywordsApply(s) {
			setMaxLen(s, uint64(v))
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
		name := semantic.StringOrIdentArg(d.Args[0])
		if name == "" {
			return
		}
		if name == semantic.FormatRaw {
			// `raw` constrains nothing: the bytes ARE the value, in
			// whatever encoding the message travels in, so `type: string,
			// format: byte` would be a lie about a field no base64
			// decoder should touch. What is left is the one line telling
			// a reader the empty schema is deliberate rather than a field
			// nobody documented.
			s.Type, s.Format = nil, ""
			s.Description = appendDescription(s.Description, rawSchemaDescription)
			return
		}
		s.Format = strfmt.OpenAPIFormat(name)
	},
	"gt":  func(d *ast.Decorator, s *openapi3.Schema) { emitExclusive(s, "exclusiveMinimum", d, 0) },
	"gte": func(d *ast.Decorator, s *openapi3.Schema) { emitBound(s, "minimum", d, 0, setMin) },
	"lt":  func(d *ast.Decorator, s *openapi3.Schema) { emitExclusive(s, "exclusiveMaximum", d, 0) },
	"lte": func(d *ast.Decorator, s *openapi3.Schema) { emitBound(s, "maximum", d, 0, setMax) },
	"range": func(d *ast.Decorator, s *openapi3.Schema) {
		emitBound(s, "minimum", d, 0, setMin)
		emitBound(s, "maximum", d, 1, setMax)
	},
	"positive": func(_ *ast.Decorator, s *openapi3.Schema) { setExclusive(s, "exclusiveMinimum", 0, nil) },
	"negative": func(_ *ast.Decorator, s *openapi3.Schema) { setExclusive(s, "exclusiveMaximum", 0, nil) },
	"multipleOf": func(d *ast.Decorator, s *openapi3.Schema) {
		if r, ok := rawIfBigInt(d, 0); ok {
			schemaExt(s, "multipleOf", r)
		} else if v, ok := numericArgValue(d, 0); ok && v != 0 {
			s.MultipleOf = &v
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

// applyFieldConstraints stamps every constraint keyword a field or scalar
// schema can carry onto s. Each row acts only when its decorator is
// present, and the semantic layer guarantees those are type-appropriate,
// so applying the whole table is safe everywhere. The map-KEY
// propertyNames builder is the deliberate exception - it omits numeric
// bounds no SDK generator honours - and applies a subset instead.
func applyFieldConstraints(ds []*ast.Decorator, s *openapi3.Schema) {
	applyConstraintFamilies(ds, s, semantic.ConstraintSchema)
}

// applyConstraintFamilies stamps the keywords of the decorators in ds whose
// family is in fams onto s.
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
