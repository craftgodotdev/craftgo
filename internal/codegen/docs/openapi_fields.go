package docs

import (
	"encoding/json"
	"strconv"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// rawIfBigInt returns argument i as an exact json.Number when it is an
// integer beyond float64's exact range.
func rawIfBigInt(d *ast.Decorator, i int) (json.Number, bool) {
	if i >= len(d.Args) {
		return "", false
	}
	if l, ok := semantic.ParseNumericArg(d.Args[i]); ok && l.IsInt && l.IsBigInt {
		return json.Number(strconv.FormatInt(l.IntVal, 10)), true
	}
	return "", false
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

func applyFieldMetadata(f *ast.Field, ref *openapi3.SchemaRef, pkg *semantic.Package) {
	if ref == nil {
		return
	}
	// A bare $ref takes no sibling keywords, so field metadata wraps it: in
	// `anyOf: [{$ref}, {type: null}]` when optional, else in an `allOf`.
	if ref.Ref != "" {
		nullable := semantic.FieldIsOptional(f)
		extra := fieldConstraintSchema(f)
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
			applyFieldConstraints(f.Decorators, w)
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
		applyFieldConstraints(f.Decorators, ref.Value)
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
	if semantic.FieldIsOptional(f) {
		applyNullable(ref.Value)
	}
	if ex, ok := semantic.ExampleValue(f, pkg); ok {
		ref.Value.Example = ex
	}
	if def, ok := semantic.ResolveDefaultValue(f, pkg); ok {
		ref.Value.Default = def
	}
	applyFieldConstraints(f.Decorators, ref.Value)
}

// fieldConstraintSchema returns a schema of the constraints f adds to the
// type it refs, or nil when it adds none.
func fieldConstraintSchema(f *ast.Field) *openapi3.Schema {
	if f == nil || !hasFieldConstraintDecorator(f.Decorators) {
		return nil
	}
	s := &openapi3.Schema{}
	applyFieldConstraints(f.Decorators, s)
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

// numericArgValue returns argument i as a float64, or false when it is not
// a number.
func numericArgValue(d *ast.Decorator, i int) (float64, bool) {
	if i >= len(d.Args) {
		return 0, false
	}
	if l, ok := semantic.ParseNumericArg(d.Args[i]); ok {
		return l.FloatVal, true
	}
	return 0, false
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

// schemaExt sets keyword key through Extensions, which marshal as plain
// keywords: kin-openapi has no 3.1 exclusive bound and no exact big integer.
func schemaExt(s *openapi3.Schema, key string, v interface{}) {
	if s.Extensions == nil {
		s.Extensions = make(map[string]interface{})
	}
	s.Extensions[key] = v
}

// curExtNumber reads Extensions key as a float64, stored as either a
// float64 or a json.Number.
func curExtNumber(s *openapi3.Schema, key string) (float64, bool) {
	if s.Extensions == nil {
		return 0, false
	}
	switch v := s.Extensions[key].(type) {
	case float64:
		return v, true
	case json.Number:
		if f, err := v.Float64(); err == nil {
			return f, true
		}
	}
	return 0, false
}

// setMin and setMax keep the tighter inclusive bound: the validator runs
// every decorator, so `@gte(10) @range(0, 100)` enforces a minimum of 10.
func setMin(s *openapi3.Schema, v float64) {
	if s.Min == nil || v > *s.Min {
		s.Min = &v
	}
}

func setMax(s *openapi3.Schema, v float64) {
	if s.Max == nil || v < *s.Max {
		s.Max = &v
	}
}

// emitBound writes argument i as an inclusive bound: a big integer exactly
// through Extensions, any other number through native.
func emitBound(s *openapi3.Schema, key string, d *ast.Decorator, i int, native func(*openapi3.Schema, float64)) {
	if r, ok := rawIfBigInt(d, i); ok {
		schemaExt(s, key, r)
		return
	}
	if v, ok := numericArgValue(d, i); ok {
		native(s, v)
	}
}

// setExclusive keeps the tighter exclusive bound (the larger minimum, the
// smaller maximum), writing raw when it is non-nil, else v.
func setExclusive(s *openapi3.Schema, key string, v float64, raw interface{}) {
	if cur, ok := curExtNumber(s, key); ok {
		if key == "exclusiveMinimum" && v <= cur {
			return
		}
		if key == "exclusiveMaximum" && v >= cur {
			return
		}
	}
	if raw != nil {
		schemaExt(s, key, raw)
	} else {
		schemaExt(s, key, v)
	}
}

// emitExclusive writes argument i as an exclusive bound: a big integer as an
// exact json.Number, any other number as a float64.
func emitExclusive(s *openapi3.Schema, key string, d *ast.Decorator, i int) {
	if r, ok := rawIfBigInt(d, i); ok {
		if f, err := r.Float64(); err == nil {
			setExclusive(s, key, f, r)
		} else {
			schemaExt(s, key, r)
		}
		return
	}
	if v, ok := numericArgValue(d, i); ok {
		setExclusive(s, key, v, nil)
	}
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
	v, ok := numericArgValue(d, 0)
	if !ok || v < 0 {
		return
	}
	u := uint64(v)
	switch {
	case s.Type != nil && s.Type.Includes("array"):
		array(u)
	case s.Type != nil && s.Type.Includes("object"):
		object(u)
	}
}
