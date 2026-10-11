package docs

import (
	"math/big"
	"strconv"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/strfmt"
	"github.com/getkin/kin-openapi/openapi3"
)

// rawSchemaDescription describes a `@format(raw)` schema, which has no type.
const rawSchemaDescription = "raw encoded value"

// schemaKeyword stamps one constraint decorator onto a schema of a value of
// DSL primitive prim.
type schemaKeyword func(d *ast.Decorator, s *openapi3.Schema, prim string)

// schemaKeywords maps each constraint decorator of [semantic.Names] with a
// schema form to its keyword; a ConstraintRuntime decorator has no row.
var schemaKeywords = map[string]schemaKeyword{
	"length":    lengthKeywords,
	"minLength": lengthKeywords,
	"maxLength": lengthKeywords,
	"pattern": func(d *ast.Decorator, s *openapi3.Schema, _ string) {
		if len(d.Args) == 1 {
			if sl, ok := d.Args[0].Value.(*ast.StringLit); ok {
				s.Pattern = sl.Value
			}
		}
	},
	"format": func(d *ast.Decorator, s *openapi3.Schema, _ string) {
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
			delete(s.Extensions, "contentEncoding")
			s.Description = appendDescription(s.Description, rawSchemaDescription)
			return
		}
		s.Format = strfmt.OpenAPIFormat(name)
	},
	"gt":       valueKeywords,
	"gte":      valueKeywords,
	"lt":       valueKeywords,
	"lte":      valueKeywords,
	"range":    valueKeywords,
	"positive": valueKeywords,
	"negative": valueKeywords,
	"multipleOf": func(d *ast.Decorator, s *openapi3.Schema, _ string) {
		if v, ok := numberArg(d, 0); ok && v.Sign() != 0 {
			setNumber(s, "multipleOf", v)
		}
	},
	"minItems": itemCountKeywords,
	"maxItems": itemCountKeywords,
	"uniqueItems": func(_ *ast.Decorator, s *openapi3.Schema, _ string) {
		if s.Type != nil && s.Type.Includes("array") {
			s.UniqueItems = true
		}
	},
}

// valueKeywords stamps a bound on a value of DSL primitive prim: the minimum
// or maximum, exclusive for a strict side, of each side of d, at 0 for a flag.
func valueKeywords(d *ast.Decorator, s *openapi3.Schema, prim string) {
	sides, _ := semantic.BoundSides(d.Name)
	args := semantic.BoundArgs(d)
	if args == nil {
		for _, side := range sides {
			tightenBound(s, boundKeyword(side), new(big.Rat))
		}
		return
	}
	for i, side := range sides {
		emitBound(s, side, args[i], prim)
	}
}

// boundKeyword returns the keyword of a numeric bound on side s.
func boundKeyword(s semantic.BoundSide) string {
	switch {
	case s.Lower && s.Strict:
		return "exclusiveMinimum"
	case s.Lower:
		return "minimum"
	case s.Strict:
		return "exclusiveMaximum"
	}
	return "maximum"
}

// lengthKeywords stamps the minLength or maxLength of each side of d, a
// length bound, on a string; `@length(N)` sets both to N. On bytes they
// would count the characters of the base64 text: [byteLengthNote] words the
// bytes the validator counts.
func lengthKeywords(d *ast.Decorator, s *openapi3.Schema, prim string) {
	sides, _ := semantic.BoundSides(d.Name)
	args := semantic.BoundArgs(d)
	if sp, _ := prims.Lookup(prim); args == nil || sp.Kind == prims.Bytes {
		return
	}
	for i, side := range sides {
		v, ok := countValue(args[i])
		switch {
		case !ok:
			return
		case side.Lower:
			setMinLen(s, v)
		default:
			setMaxLen(s, v)
		}
	}
}

// itemCountKeywords stamps d, an item-count bound, as minItems or maxItems on
// an array, minProperties or maxProperties on a map.
func itemCountKeywords(d *ast.Decorator, s *openapi3.Schema, _ string) {
	sides, _ := semantic.BoundSides(d.Name)
	args := semantic.BoundArgs(d)
	if len(args) != 1 {
		return
	}
	if sides[0].Lower {
		itemCountKeyword(s, args[0], func(u uint64) { s.MinItems = u }, func(u uint64) { s.MinProps = u })
		return
	}
	itemCountKeyword(s, args[0], func(u uint64) { s.MaxItems = &u }, func(u uint64) { s.MaxProps = &u })
}

// applyFieldConstraints stamps the keyword of every constraint in ds that
// has a schema form onto s, the schema of a value of DSL primitive prim, and
// a bytes value's length after its description.
func applyFieldConstraints(ds []*ast.Decorator, s *openapi3.Schema, prim string) {
	applyConstraintFamilies(ds, s, semantic.ConstraintSchema, prim)
	if note := byteLengthNote(ds, prim); note != "" {
		s.Description = appendDescription(s.Description, note)
	}
}

// byteLengthNote words the length the decorators ds give a value of DSL
// primitive prim when it is bytes, in the bytes the validator counts, or
// returns "": no schema keyword counts the bytes a base64 text decodes to.
func byteLengthNote(ds []*ast.Decorator, prim string) string {
	if sp, _ := prims.Lookup(prim); sp.Kind != prims.Bytes {
		return ""
	}
	var counted openapi3.Schema
	applyConstraintFamilies(ds, &counted, semantic.ConstraintLength, "string")
	lo, hi := counted.MinLength, counted.MaxLength
	switch {
	case hi == nil && lo == 0:
		return ""
	case hi == nil:
		return "Decodes to at least " + byteCount(lo) + "."
	case lo == 0:
		return "Decodes to at most " + byteCount(*hi) + "."
	case lo == *hi:
		return "Decodes to exactly " + byteCount(lo) + "."
	}
	return "Decodes to between " + strconv.FormatUint(lo, 10) + " and " + byteCount(*hi) + "."
}

// byteCount spells n bytes.
func byteCount(n uint64) string {
	if n == 1 {
		return "1 byte"
	}
	return strconv.FormatUint(n, 10) + " bytes"
}

// applyConstraintFamilies stamps onto s, the schema of a value of DSL
// primitive prim, the keywords of the decorators in ds whose family is in fams.
func applyConstraintFamilies(ds []*ast.Decorator, s *openapi3.Schema, fams semantic.ConstraintFamily, prim string) {
	if s == nil {
		return
	}
	for _, d := range ds {
		if d == nil {
			continue
		}
		if kw := schemaKeywords[d.Name]; kw != nil && semantic.ConstraintOf(d.Name)&fams != 0 {
			kw(d, s, prim)
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
