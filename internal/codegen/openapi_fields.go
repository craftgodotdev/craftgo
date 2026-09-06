// Field-level decorator -> schema metadata mapping.
package codegen

import (
	"encoding/json"
	"strconv"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// rawIfBigInt returns the exact decimal text of an integer-literal bound
// whose magnitude exceeds float64's exact range, as a json.Number to be
// emitted verbatim through Extensions. For in-range or non-integer args it
// returns ok=false so the caller takes the ordinary float64 path. The
// big-integer classification is shared with the validator via
// [parseNumericArg] so the two sides agree on which bounds need exact text.
func rawIfBigInt(d *ast.Decorator, i int) (json.Number, bool) {
	if i >= len(d.Args) {
		return "", false
	}
	if l, ok := parseNumericArg(d.Args[i]); ok && l.isInt && l.isBigInt {
		return json.Number(strconv.FormatInt(l.intVal, 10)), true
	}
	return "", false
}

// stampDeprecated marks s deprecated when the field carries @deprecated and
// appends any reason to its description. Shared by applyFieldMetadata's three
// schema-shape branches so the stamp is spelled once.
func stampDeprecated(s *openapi3.Schema, decs []*ast.Decorator) {
	if !hasDeprecatedDecorator(decs) {
		return
	}
	s.Deprecated = true
	if reason := deprecatedReason(decs); reason != "" {
		s.Description = appendDescription(s.Description, "Deprecated: "+reason)
	}
}

func applyFieldMetadata(f *ast.Field, ref *openapi3.SchemaRef, pkg *semantic.Package) {
	if ref == nil {
		return
	}
	// Plain $ref: the referenced schema carries the type's own
	// description / example / constraints. A bare $ref can't carry
	// sibling keywords portably, so any FIELD-LEVEL metadata forces a
	// wrapper:
	//   - @nullable / `?`   -> anyOf:[{$ref}, {type:null}] (the value may
	//     be null; a `@nullable`-without-`?` field stays in `required`,
	//     matching the Go struct that always emits the key as JSON null).
	//   - narrowing constraint (`unitCents Cents @lte(1000000)`) -> allOf:
	//     [{$ref}, {constraints}], so a field that tightens the referenced
	//     type advertises the bound the runtime validator enforces.
	//   - @default / @deprecated -> carried on the wrapper.
	//   - field-level @doc / @example -> carried on the wrapper as
	//     siblings of the allOf/anyOf, so a field that documents or
	//     exemplifies the referenced type keeps that metadata instead of
	//     dropping it (a bare $ref has nowhere portable to hang them).
	if ref.Ref != "" {
		nullable := hasNullableDecorator(f.Decorators) || (f.Type != nil && f.Type.Optional)
		extra := fieldConstraintSchema(f)
		def, hasDef := resolveDefaultValue(f, pkg)
		deprecated := hasDeprecatedDecorator(f.Decorators)
		desc := resolveDescription(f.Decorators, f.Doc)
		ex, hasEx := exampleValue(f, pkg)
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
	// Optional-ref wrapper (anyOf:[$ref, {type:null}]). Field-level
	// metadata lands on the wrapper as siblings of the anyOf: the
	// field's @doc / @example document this specific use of the
	// referenced type, and `default` / narrowing constraints are ANDed
	// with the resolved value (a numeric bound is vacuous for the `null`
	// branch), keeping the spec in step with the runtime validator.
	if isNullableRefWrapper(ref.Value) {
		if desc := resolveDescription(f.Decorators, f.Doc); desc != "" {
			ref.Value.Description = desc
		}
		if ex, ok := exampleValue(f, pkg); ok {
			ref.Value.Example = ex
		}
		if def, ok := resolveDefaultValue(f, pkg); ok {
			ref.Value.Default = def
		}
		stampDeprecated(ref.Value, f.Decorators)
		applyFieldConstraints(f.Decorators, ref.Value)
		return
	}
	if desc := resolveDescription(f.Decorators, f.Doc); desc != "" {
		ref.Value.Description = desc
	}
	if hasDeprecatedDecorator(f.Decorators) {
		ref.Value.Deprecated = true
		if reason := deprecatedReason(f.Decorators); reason != "" {
			ref.Value.Description = appendDescription(ref.Value.Description, "Deprecated: "+reason)
		}
	}
	if hasNullableDecorator(f.Decorators) || (f.Type != nil && f.Type.Optional) {
		applyNullable(ref.Value)
	}
	if ex, ok := exampleValue(f, pkg); ok {
		ref.Value.Example = ex
	}
	if def, ok := resolveDefaultValue(f, pkg); ok {
		ref.Value.Default = def
	}
	applyFieldConstraints(f.Decorators, ref.Value)
}

// fieldConstraintSchema builds a schema carrying ONLY the field-level
// narrowing constraints (numeric / string-length / pattern / format) a
// field stacks on top of a referenced type, or nil when it declares
// none. A $ref field is never an array (arrays render as
// `{type: array, items: {$ref}}`), so the array keywords are not
// applicable here.
func fieldConstraintSchema(f *ast.Field) *openapi3.Schema {
	if f == nil || !hasFieldConstraintDecorator(f.Decorators) {
		return nil
	}
	s := &openapi3.Schema{}
	applyFieldConstraints(f.Decorators, s)
	return s
}

// isNullableRefWrapper recognises the `anyOf: [{$ref}, {type: null}]`
// shape that schemaForTypeRef emits for an optional named-type (or
// optional generic-instance) field - the OpenAPI 3.1 idiom for "ref OR
// null" (3.1 dropped the `nullable` keyword, and a bare $ref still can
// not carry sibling validators portably). Metadata on the wrapper is
// interpreted inconsistently by clients, so callers branch on this
// signature to avoid stamping description/example on it.
func isNullableRefWrapper(s *openapi3.Schema) bool {
	if s == nil || len(s.AnyOf) != 2 || s.Type != nil || len(s.Properties) != 0 {
		return false
	}
	return s.AnyOf[0].Ref != "" && isNullTypeSchema(s.AnyOf[1].Value)
}

// isNullTypeSchema reports whether s is exactly the 3.1 null sentinel
// (`type: "null"` with no other shape) used as the second branch of a
// nullable-ref wrapper's anyOf.
func isNullTypeSchema(s *openapi3.Schema) bool {
	return s != nil && s.Type != nil && s.Type.Is("null")
}

// numericArgValue pulls the i-th positional argument as a float64.
// Accepts both IntLit and FloatLit so callers don't have to switch on
// type. Returns (0, false) for any other kind of literal so the OpenAPI
// emitter silently skips invalid args.
func numericArgValue(d *ast.Decorator, i int) (float64, bool) {
	if i >= len(d.Args) {
		return 0, false
	}
	if l, ok := parseNumericArg(d.Args[i]); ok {
		return l.floatVal, true
	}
	return 0, false
}

// applyNullable marks a value schema as nullable using the OpenAPI 3.1
// canonical form: it appends "null" to the schema's `type` list
// (`type: [string, "null"]`). OpenAPI 3.1 REMOVED the 3.0 boolean
// `nullable: true` keyword, so emitting it inside a doc that declares
// `openapi: 3.1.0` makes every 3.1-aware client generator (hey-api,
// openapi-typescript, openapi-generator >=7, Swagger UI 3.1) silently
// drop the null union - `bio string @nullable` then types as `string`
// on the client instead of `string | null`.
//
// Only typed value schemas pass through here; named-ref nullability has
// no `type` list to extend and is handled in [schemaForTypeRef] via the
// `anyOf: [{$ref}, {type: null}]` wrapper instead.
//
// craftgo never runs kin-openapi's `T.Validate()` on the emitted doc,
// so that library's lagging rejection of the 3.1 null-array form does
// not apply here.
func applyNullable(s *openapi3.Schema) {
	if s == nil || s.Type == nil {
		return
	}
	if !s.Type.Includes("null") {
		*s.Type = append(*s.Type, "null")
	}
}

// appendDescription joins a new note onto an existing description with
// a single blank-line separator. Empty existing description means the
// note becomes the entire description; empty note is a no-op.
func appendDescription(existing, note string) string {
	if note == "" {
		return existing
	}
	if existing == "" {
		return note
	}
	return existing + "\n\n" + note
}

// schemaExt sets a raw schema keyword through Extensions, which marshal as
// plain keywords - the route for the OpenAPI 3.1 numeric forms kin-openapi
// still models as 3.0 booleans, and for big-integer literals that must
// survive as exact json.Number values.
func schemaExt(s *openapi3.Schema, key string, v interface{}) {
	if s.Extensions == nil {
		s.Extensions = make(map[string]interface{})
	}
	s.Extensions[key] = v
}

// curExtNumber reads the current numeric value of an Extensions key as a
// float64, handling both the float64 and json.Number representations.
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

// setMin / setMax intersect an inclusive bound rather than overwrite it:
// the runtime validator runs EVERY decorator (tightest bound wins), so
// stacking `@gte(10) @range(0,100)` enforces min 10 at runtime - the spec
// must advertise the same, not the last writer's looser 0.
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

// emitBound writes an inclusive minimum / maximum: a big integer literal
// rides through Extensions as a raw json.Number (exact), everything else
// uses the native float64 field so existing specs are unchanged.
func emitBound(s *openapi3.Schema, key string, d *ast.Decorator, i int, native func(*openapi3.Schema, float64)) {
	if r, ok := rawIfBigInt(d, i); ok {
		schemaExt(s, key, r)
		return
	}
	if v, ok := numericArgValue(d, i); ok {
		native(s, v)
	}
}

// setExclusive intersects an exclusive bound: the runtime runs EVERY
// decorator, so the tightest wins - the LARGEST exclusiveMinimum and the
// SMALLEST exclusiveMaximum. Without this, stacking `@gt(5) @positive`
// (or `@lt(-5) @negative`) would advertise the LOOSER last-writer bound
// (exclusiveMinimum 0) while the validator enforces the tighter one.
// Exclusive bounds always ride through Extensions as numbers (kin-openapi
// still models ExclusiveMin/Max as the 3.0 booleans).
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

// emitExclusive writes an exclusive bound from a decorator argument: big
// integers ride as a raw json.Number, smaller values as a float64.
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

// setMinLen / setMaxLen intersect a string-length bound (tightest wins),
// matching the runtime which runs every decorator: `@length(5)
// @minLength(3) @maxLength(10)` enforces exactly 5, so the spec must too.
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

// lengthKeywordsApply reports whether string-length keywords belong on s.
// `bytes` renders as `{type: string, format: byte}` (a base64 string):
// `minLength` / `maxLength` there would constrain the BASE64-encoded
// character count, whereas the runtime validator (and the author's intent)
// count RAW bytes - so the keyword would advertise a different bound than
// the server enforces. JSON Schema has no decoded-byte-length keyword, so
// the constraint is left to the runtime rather than advertised incorrectly.
func lengthKeywordsApply(s *openapi3.Schema) bool { return s.Format != "byte" }

// itemCountKeyword stores an item-count bound on the keyword matching the
// schema's shape: array fields count elements via minItems / maxItems, map
// (object) fields count entries via minProperties / maxProperties. A
// composition wrapper (the `anyOf:[{$ref}, {null}]` of a nullable
// named-type field) is neither, so it gets nothing - emitting
// minProperties there advertises an unenforced, unsatisfiable constraint.
// Includes, not Is, because an optional array is `type: [array, "null"]`.
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
