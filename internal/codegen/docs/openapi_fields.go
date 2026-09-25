package docs

import (
	"encoding/json"
	"math/big"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// numberArg returns argument i as the exact value it writes; ok is false for
// a non-number.
func numberArg(d *ast.Decorator, i int) (*big.Rat, bool) {
	if i >= len(d.Args) {
		return nil, false
	}
	l, ok := semantic.ParseNumericArg(d.Args[i])
	if !ok {
		return nil, false
	}
	r := l.Rat()
	return r, r != nil
}

// countArg returns argument i as a count, a whole number up to the uint64
// limit.
func countArg(d *ast.Decorator, i int) (uint64, bool) {
	r, ok := numberArg(d, i)
	if !ok || !r.IsInt() || r.Sign() < 0 || !r.Num().IsUint64() {
		return 0, false
	}
	return r.Num().Uint64(), true
}

// stampDeprecated marks s deprecated when decs carry @deprecated, appending
// the reason to its description.
func stampDeprecated(s *openapi3.Schema, decs []*ast.Decorator) {
	if !semantic.IsDeprecated(decs) {
		return
	}
	s.Deprecated = true
	if reason := semantic.DeprecatedReason(decs); reason != "" {
		s.Description = appendDescription(s.Description, "Deprecated: "+reason)
	}
}

// applyFieldMetadata stamps f's docs, default, example and constraints onto
// ref, the schema of its value, which admits null when nullable.
func applyFieldMetadata(f *ast.Field, ref *openapi3.SchemaRef, pkg *semantic.Package, nullable bool) {
	if ref == nil {
		return
	}
	prim := semantic.ResolveField(f, pkg, nil).ResolvedPrim
	// A bare $ref takes no sibling keywords, so field metadata wraps it: in
	// `anyOf: [{$ref}, {type: null}]` when nullable, else in an `allOf`.
	if ref.Ref != "" {
		extra := fieldConstraintSchema(f, prim)
		def, hasDef := semantic.ResolveDefaultValue(f, pkg)
		deprecated := semantic.IsDeprecated(f.Decorators)
		desc := semantic.Description(f.Decorators, f.Doc)
		ex, hasEx := semantic.ExampleValue(f, pkg)
		if !nullable && extra == nil && !hasDef && !deprecated && desc == "" && !hasEx {
			return
		}
		base := ref.Ref
		ref.Ref = ""
		w := &openapi3.Schema{}
		switch {
		case nullable:
			w.AnyOf = openapi3.SchemaRefs{
				{Ref: base},
				nullSchemaRef(),
			}
			applyFieldConstraints(f.Decorators, w, prim)
		case extra != nil:
			w.AllOf = openapi3.SchemaRefs{{Ref: base}, {Value: extra}}
		default:
			w.AllOf = openapi3.SchemaRefs{{Ref: base}}
		}
		if desc != "" {
			w.Description = desc
		}
		if hasDef {
			w.Default = def
		}
		stampDeprecated(w, f.Decorators)
		if hasEx {
			w.Example = ex
		}
		ref.Value = w
		return
	}
	if ref.Value == nil {
		return
	}
	// An optional ref's wrapper takes the field metadata beside its anyOf.
	if isNullableRefWrapper(ref.Value) {
		if desc := semantic.Description(f.Decorators, f.Doc); desc != "" {
			ref.Value.Description = desc
		}
		if ex, ok := semantic.ExampleValue(f, pkg); ok {
			ref.Value.Example = ex
		}
		if def, ok := semantic.ResolveDefaultValue(f, pkg); ok {
			ref.Value.Default = def
		}
		stampDeprecated(ref.Value, f.Decorators)
		applyFieldConstraints(f.Decorators, ref.Value, prim)
		return
	}
	if desc := semantic.Description(f.Decorators, f.Doc); desc != "" {
		ref.Value.Description = desc
	}
	if semantic.IsDeprecated(f.Decorators) {
		ref.Value.Deprecated = true
		if reason := semantic.DeprecatedReason(f.Decorators); reason != "" {
			ref.Value.Description = appendDescription(ref.Value.Description, "Deprecated: "+reason)
		}
	}
	if nullable {
		applyNullable(ref.Value)
	}
	if ex, ok := semantic.ExampleValue(f, pkg); ok {
		ref.Value.Example = ex
	}
	if def, ok := semantic.ResolveDefaultValue(f, pkg); ok {
		ref.Value.Default = def
	}
	applyFieldConstraints(f.Decorators, ref.Value, prim)
}

// fieldConstraintSchema returns a schema of the constraints f, a value of DSL
// primitive prim, adds to the type it refs, or nil when it adds none.
func fieldConstraintSchema(f *ast.Field, prim string) *openapi3.Schema {
	if f == nil || !hasFieldConstraintDecorator(f.Decorators) {
		return nil
	}
	s := &openapi3.Schema{}
	applyFieldConstraints(f.Decorators, s, prim)
	return s
}

// isNullableRefWrapper reports whether s is the [nullableRef] wrapper,
// `anyOf: [{$ref}, {type: null}]`.
func isNullableRefWrapper(s *openapi3.Schema) bool {
	if s == nil || len(s.AnyOf) != 2 || s.Type != nil || len(s.Properties) != 0 {
		return false
	}
	return s.AnyOf[0].Ref != "" && isNullTypeSchema(s.AnyOf[1].Value)
}

// isNullTypeSchema reports whether s has the lone type "null".
func isNullTypeSchema(s *openapi3.Schema) bool {
	return s != nil && s.Type != nil && s.Type.Is("null")
}

// applyNullable adds "null" to s's type list: OpenAPI 3.1 has no `nullable`.
// A ref has no type list and takes the [nullableRef] wrapper.
func applyNullable(s *openapi3.Schema) {
	if s == nil || s.Type == nil {
		return
	}
	if !s.Type.Includes("null") {
		*s.Type = append(*s.Type, "null")
	}
}

// appendDescription joins note onto existing after a blank line; when either
// is empty the result is the other.
func appendDescription(existing, note string) string {
	if note == "" {
		return existing
	}
	if existing == "" {
		return note
	}
	return existing + "\n\n" + note
}

// numberField returns the kin-openapi field of numeric keyword key, nil for a
// 3.1 exclusive bound, which has none.
func numberField(s *openapi3.Schema, key string) **float64 {
	switch key {
	case "minimum":
		return &s.Min
	case "maximum":
		return &s.Max
	case "multipleOf":
		return &s.MultipleOf
	}
	return nil
}

// schemaNumber returns the value of numeric keyword key on s.
func schemaNumber(s *openapi3.Schema, key string) (*big.Rat, bool) {
	var r *big.Rat
	if f := numberField(s, key); f != nil && *f != nil {
		r = new(big.Rat).SetFloat64(**f)
	} else {
		switch v := s.Extensions[key].(type) {
		case float64:
			r = new(big.Rat).SetFloat64(v)
		case json.Number:
			r, _ = new(big.Rat).SetString(string(v))
		}
	}
	return r, r != nil
}

// exactFloatInts bounds the integers a float64 holds and prints digit for
// digit: 2^53.
var exactFloatInts = new(big.Int).Lsh(big.NewInt(1), 53)

// setNumber writes v as numeric keyword key: an integer beyond 2^53 through
// Extensions, which marshal as plain keywords, as its exact digits, which a
// float64 would round; any other number as a float64, in the kin-openapi
// field for key when there is one.
func setNumber(s *openapi3.Schema, key string, v *big.Rat) {
	delete(s.Extensions, key)
	field := numberField(s, key)
	if field != nil {
		*field = nil
	}
	var value any
	if v.IsInt() && v.Num().CmpAbs(exactFloatInts) > 0 {
		value = json.Number(v.Num().String())
	} else {
		f, _ := v.Float64()
		if field != nil {
			*field = &f
			return
		}
		value = f
	}
	if s.Extensions == nil {
		s.Extensions = map[string]any{}
	}
	s.Extensions[key] = value
}

// tightenBound sets bound key, `minimum`, `maximum` or an exclusive one, to v
// unless s holds a tighter one: the validator runs every decorator, so
// `@gte(10) @range(0, 100)` enforces a minimum of 10.
func tightenBound(s *openapi3.Schema, key string, v *big.Rat) {
	if cur, ok := schemaNumber(s, key); ok {
		c := v.Cmp(cur)
		if key == "minimum" || key == "exclusiveMinimum" {
			c = -c
		}
		if c >= 0 {
			return
		}
	}
	setNumber(s, key, v)
}

// emitBound tightens bound key with argument i of d, a bound on a value of
// DSL primitive prim.
func emitBound(s *openapi3.Schema, key string, d *ast.Decorator, i int, prim string) {
	v, ok := numberArg(d, i)
	if !ok {
		return
	}
	if sp, _ := prims.Lookup(prim); sp.Kind == prims.Float {
		v = floatBound(key, v, d.Args[i], sp.Bits)
	}
	tightenBound(s, key, v)
}

// floatBound returns bound key for literal, argument a on a float of width bits,
// judging the literal and the float the validator checks as the validator does.
func floatBound(key string, literal *big.Rat, a *ast.DecoratorArg, bits int) *big.Rat {
	l, _ := semantic.ParseNumericArg(a)
	constant, ok := new(big.Rat).SetString(l.Text())
	if !ok {
		return literal
	}
	checked, sent := atWidth(constant, bits), atWidth(literal, bits)
	if checked == nil || sent == nil {
		return literal
	}
	lower := key == "minimum" || key == "exclusiveMinimum"
	c := sent.Cmp(checked)
	admitsLiteral := c == 0 && (key == "minimum" || key == "maximum") || c > 0 && lower || c < 0 && !lower
	literalLooser := (literal.Cmp(checked) < 0) == lower
	if admitsLiteral == literalLooser {
		return literal
	}
	return checked
}

// atWidth returns r rounded to the nearest float of width bits, as Go rounds
// a constant and parses a request's value; nil past the float's range.
func atWidth(r *big.Rat, bits int) *big.Rat {
	f, _ := r.Float64()
	if bits == 32 {
		f32, _ := r.Float32()
		f = float64(f32)
	}
	return new(big.Rat).SetFloat64(f)
}

// setMinLen and setMaxLen keep the tighter string-length bound.
func setMinLen(s *openapi3.Schema, v uint64) {
	if v > s.MinLength {
		s.MinLength = v
	}
}

func setMaxLen(s *openapi3.Schema, v uint64) {
	if s.MaxLength == nil || v < *s.MaxLength {
		s.MaxLength = &v
	}
}

// lengthKeywordsApply reports whether length keywords fit s: on `format: byte`
// they would count base64 characters, not the raw bytes the validator counts.
func lengthKeywordsApply(s *openapi3.Schema) bool { return s.Format != "byte" }

// itemCountKeyword stores an item count through array or object by s's type,
// matched with Includes so an optional `[array, "null"]` counts as an array.
func itemCountKeyword(s *openapi3.Schema, d *ast.Decorator, array func(uint64), object func(uint64)) {
	u, ok := countArg(d, 0)
	if !ok {
		return
	}
	switch {
	case s.Type != nil && s.Type.Includes("array"):
		array(u)
	case s.Type != nil && s.Type.Includes("object"):
		object(u)
	}
}
