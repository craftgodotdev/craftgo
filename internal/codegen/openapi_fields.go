// Field-level decorator -> schema metadata mapping.
package codegen

import (
	"github.com/getkin/kin-openapi/openapi3"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

func applyFieldMetadata(f *ast.Field, ref *openapi3.SchemaRef) {
	if ref == nil {
		return
	}
	// Plain $ref: description / example / the type's own constraints come
	// from the referenced schema. A field-level decorator that NARROWS
	// the referenced type (`unitCents Cents @lte(1000000)`) is
	// field-specific and the runtime validator enforces it, so the spec
	// must too — emit allOf:[{$ref}, {constraints}] when such decorators
	// are present. A bare $ref can't carry sibling validators portably.
	if ref.Ref != "" {
		if extra := fieldConstraintSchema(f); extra != nil {
			ref.Value = &openapi3.Schema{
				AllOf: openapi3.SchemaRefs{
					{Ref: ref.Ref},
					{Value: extra},
				},
			}
			ref.Ref = ""
		}
		return
	}
	if ref.Value == nil {
		return
	}
	// Optional-ref wrapper (anyOf:[$ref, {type:null}]). Cosmetic
	// metadata like description/example would land on the wrapper, which
	// tooling renders inconsistently - some UI generators show it next
	// to the field, others let the $ref's own description win. We drop
	// those decorators here; users who need a field-specific
	// description should alias the type (`type Manager = User`).
	// `default` and the narrowing constraints stay: as siblings of the
	// anyOf they are ANDed with the resolved value (a numeric bound is
	// vacuous for the `null` branch), keeping the spec in step with the
	// runtime validator.
	if isNullableRefWrapper(ref.Value) {
		if def, ok := defaultValue(f.Decorators); ok {
			ref.Value.Default = def
		}
		applyNumericConstraints(f.Decorators, ref.Value)
		applyStringLengthConstraints(f.Decorators, ref.Value)
		applyPatternFormat(f.Decorators, ref.Value)
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
	if ex, ok := exampleValue(f.Decorators); ok {
		ref.Value.Example = ex
	}
	if def, ok := defaultValue(f.Decorators); ok {
		ref.Value.Default = def
	}
	applyNumericConstraints(f.Decorators, ref.Value)
	applyStringLengthConstraints(f.Decorators, ref.Value)
	applyArrayConstraints(f.Decorators, ref.Value)
	applyPatternFormat(f.Decorators, ref.Value)
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
	applyNumericConstraints(f.Decorators, s)
	applyStringLengthConstraints(f.Decorators, s)
	applyPatternFormat(f.Decorators, s)
	return s
}

// hasFieldConstraintDecorator reports whether ds carries any decorator
// that maps to an OpenAPI validation keyword (the narrowing constraints).
func hasFieldConstraintDecorator(ds []*ast.Decorator) bool {
	for _, d := range ds {
		if d == nil {
			continue
		}
		switch d.Name {
		case "gte", "gt", "lte", "lt", "range", "positive", "negative", "multipleOf",
			"minLength", "maxLength", "length", "pattern", "format":
			return true
		}
	}
	return false
}

// isNullableRefWrapper recognises the `anyOf: [{$ref}, {type: null}]`
// shape that schemaForTypeRef emits for an optional named-type (or
// optional generic-instance) field — the OpenAPI 3.1 idiom for "ref OR
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

// applyNumericConstraints translates the numeric comparison decorators
// onto an OpenAPI schema using the OpenAPI 3.1 (JSON Schema 2020-12)
// keyword shapes:
//
//	@gte(N) → minimum: N
//	@gt(N)  → exclusiveMinimum: N        (a NUMBER, not a boolean)
//	@lte(N) → maximum: N
//	@lt(N)  → exclusiveMaximum: N        (a NUMBER, not a boolean)
//	@range(lo, hi) → minimum: lo, maximum: hi (both inclusive)
//	@multipleOf(N) → multipleOf: N
//	@positive → exclusiveMinimum: 0
//	@negative → exclusiveMaximum: 0
//
// In 3.1 the exclusive bounds ARE the numeric limit (they replace the
// 3.0 `minimum: N + exclusiveMinimum: true` pair). kin-openapi still
// models `ExclusiveMin/Max` as the 3.0 booleans, so the numeric form is
// emitted through Extensions, which marshal as raw schema keywords. A
// 3.1 validator / client generator (hey-api, openapi-typescript,
// openapi-generator >=7) rejects the boolean form with
// "'exclusiveMinimum' value must be a number". craftgo never runs
// kin-openapi's own (3.0-era) validator on the emitted doc, so its
// lagging support for the numeric form does not apply.
//
// Without this wiring, client generators see the field as an unbounded
// `number` and produce types that allow values the server rejects at
// validate time.
func applyNumericConstraints(ds []*ast.Decorator, s *openapi3.Schema) {
	if s == nil {
		return
	}
	setExclusive := func(key string, v float64) {
		if s.Extensions == nil {
			s.Extensions = make(map[string]interface{})
		}
		s.Extensions[key] = v
	}
	for _, d := range ds {
		if d == nil {
			continue
		}
		switch d.Name {
		case "gte":
			if v, ok := numericArgValue(d, 0); ok {
				s.Min = &v
			}
		case "gt":
			if v, ok := numericArgValue(d, 0); ok {
				setExclusive("exclusiveMinimum", v)
			}
		case "lte":
			if v, ok := numericArgValue(d, 0); ok {
				s.Max = &v
			}
		case "lt":
			if v, ok := numericArgValue(d, 0); ok {
				setExclusive("exclusiveMaximum", v)
			}
		case "range":
			if lo, ok := numericArgValue(d, 0); ok {
				s.Min = &lo
			}
			if hi, ok := numericArgValue(d, 1); ok {
				s.Max = &hi
			}
		case "positive":
			setExclusive("exclusiveMinimum", 0)
		case "negative":
			setExclusive("exclusiveMaximum", 0)
		case "multipleOf":
			if v, ok := numericArgValue(d, 0); ok && v != 0 {
				s.MultipleOf = &v
			}
		}
	}
}

// applyStringLengthConstraints maps `@length`/`@minLength`/`@maxLength`
// to the OpenAPI string keywords. Skipped on non-string schemas: caller
// has the field context, but emitting these on, say, a numeric schema
// would still validate (kin-openapi tolerates) — the guard is cheap.
func applyStringLengthConstraints(ds []*ast.Decorator, s *openapi3.Schema) {
	if s == nil {
		return
	}
	for _, d := range ds {
		if d == nil {
			continue
		}
		switch d.Name {
		case "minLength":
			if v, ok := numericArgValue(d, 0); ok && v >= 0 {
				u := uint64(v)
				s.MinLength = u
			}
		case "maxLength":
			if v, ok := numericArgValue(d, 0); ok && v >= 0 {
				u := uint64(v)
				s.MaxLength = &u
			}
		case "length":
			if lo, ok := numericArgValue(d, 0); ok && lo >= 0 {
				s.MinLength = uint64(lo)
			}
			if hi, ok := numericArgValue(d, 1); ok && hi >= 0 {
				u := uint64(hi)
				s.MaxLength = &u
			}
		}
	}
}

// applyArrayConstraints maps `@minItems` / `@maxItems` / `@uniqueItems`
// to the OpenAPI array keywords. No-op on non-array schemas — the
// caller's field context disambiguates but the schema itself doesn't
// reject these keywords on non-array shapes, so guarding here keeps
// the spec clean.
func applyArrayConstraints(ds []*ast.Decorator, s *openapi3.Schema) {
	if s == nil {
		return
	}
	// Array fields count elements via minItems/maxItems; map (object)
	// fields count entries via minProperties/maxProperties — the same
	// decorators, but a different JSON-Schema keyword per underlying
	// shape (minItems on an object is invalid). Includes, not Is, because
	// an optional array is `type: [array, "null"]`. @uniqueItems is
	// array-only (no object analogue), so it is dropped on a map.
	isArray := s.Type != nil && s.Type.Includes("array")
	for _, d := range ds {
		if d == nil {
			continue
		}
		switch d.Name {
		case "minItems":
			if v, ok := numericArgValue(d, 0); ok && v >= 0 {
				u := uint64(v)
				if isArray {
					s.MinItems = u
				} else {
					s.MinProps = u
				}
			}
		case "maxItems":
			if v, ok := numericArgValue(d, 0); ok && v >= 0 {
				u := uint64(v)
				if isArray {
					s.MaxItems = &u
				} else {
					s.MaxProps = &u
				}
			}
		case "uniqueItems":
			if isArray {
				s.UniqueItems = true
			}
		}
	}
}

// applyPatternFormat maps `@pattern("...")` and `@format(name)` to
// the OpenAPI keywords of the same name. Must be called for every
// field-level schema; scalar component schemas have their own emit
// path that sets these directly.
func applyPatternFormat(ds []*ast.Decorator, s *openapi3.Schema) {
	if s == nil {
		return
	}
	for _, d := range ds {
		if d == nil {
			continue
		}
		switch d.Name {
		case "pattern":
			if len(d.Args) == 1 {
				if sl, ok := d.Args[0].Value.(*ast.StringLit); ok {
					s.Pattern = sl.Value
				}
			}
		case "format":
			if len(d.Args) == 1 {
				switch v := d.Args[0].Value.(type) {
				case *ast.StringLit:
					s.Format = v.Value
				case *ast.IdentExpr:
					if v.Name != nil {
						s.Format = v.Name.String()
					}
				}
			}
		}
	}
}

// numericArgValue pulls the i-th positional argument as a float64.
// Accepts both IntLit and FloatLit so callers don't have to switch on
// type. Returns (0, false) for any other kind of literal so the OpenAPI
// emitter silently skips invalid args.
func numericArgValue(d *ast.Decorator, i int) (float64, bool) {
	if i >= len(d.Args) {
		return 0, false
	}
	switch v := d.Args[i].Value.(type) {
	case *ast.IntLit:
		return float64(v.Value), true
	case *ast.FloatLit:
		return v.Value, true
	}
	return 0, false
}

// defaultValue extracts a `@default(v)` argument as a typed Go value
// suitable for `openapi3.Schema.Default`. Mirrors [exampleValue] but
// keyed off the `@default` decorator. Returns (nil, false) when no
// default decorator is present so the caller leaves the schema
// untouched. Bare-ident defaults (e.g. `@default(Active)` for an enum)
// resolve to the ident's spelling - OpenAPI consumers see the wire
// value the runtime would emit.
func defaultValue(ds []*ast.Decorator) (any, bool) {
	for _, d := range ds {
		if d.Name != "default" || len(d.Args) == 0 {
			continue
		}
		v := d.Args[0].Value
		if ident, ok := v.(*ast.IdentExpr); ok && ident.Name != nil {
			return ident.Name.String(), true
		}
		return literalToAny(v)
	}
	return nil, false
}

// exampleValue extracts an `@example(v)` argument as a typed Go value
// suitable for `openapi3.Schema.Example`. Strings, ints, floats, and
// booleans all round-trip through YAML correctly when assigned to the
// `any` Example field. Array literals (`@example(["a", "b"])`) become
// `[]any` so array-typed properties get a sensible YAML rendering.
// Returns (nil, false) when no example decorator is present so the
// caller leaves the schema untouched.
func exampleValue(ds []*ast.Decorator) (any, bool) {
	for _, d := range ds {
		if d.Name != "example" || len(d.Args) == 0 {
			continue
		}
		return literalToAny(d.Args[0].Value)
	}
	return nil, false
}

// literalToAny converts an [ast.Expr] literal into the equivalent Go
// runtime value. Arrays recurse so nested literals (e.g. an array of
// ints) round-trip without losing element types. Unsupported nodes
// return (nil, false) and the caller skips emission.
func literalToAny(e ast.Expr) (any, bool) {
	switch v := e.(type) {
	case *ast.StringLit:
		return v.Value, true
	case *ast.IntLit:
		return v.Value, true
	case *ast.FloatLit:
		return v.Value, true
	case *ast.BoolLit:
		return v.Value, true
	case *ast.NullLit:
		return nil, true
	case *ast.ArrayLit:
		out := make([]any, 0, len(v.Elements))
		for _, el := range v.Elements {
			x, ok := literalToAny(el)
			if !ok {
				return nil, false
			}
			out = append(out, x)
		}
		return out, true
	}
	return nil, false
}

// hasNullableDecorator reports whether `@nullable` appears on the
// field. The DSL already has `T?` for "optional" (field can be absent);
// `@nullable` is the orthogonal "value can be null when present" flag,
// surfaced via OpenAPI's null-type entry so spec consumers know
// `null` is a valid wire value.
func hasNullableDecorator(ds []*ast.Decorator) bool { return ast.HasDecorator(ds, "nullable") }

// hasSensitiveDecorator reports whether ds contains the `@sensitive`
// marker. Sensitive fields are server-only: they get `json:"-"` in
// the Go struct (so neither the JSON decoder nor the encoder touches
// them) and are skipped entirely from the OpenAPI spec.
func hasSensitiveDecorator(ds []*ast.Decorator) bool { return ast.HasDecorator(ds, "sensitive") }

// applyNullable marks a value schema as nullable using the OpenAPI 3.1
// canonical form: it appends "null" to the schema's `type` list
// (`type: [string, "null"]`). OpenAPI 3.1 REMOVED the 3.0 boolean
// `nullable: true` keyword, so emitting it inside a doc that declares
// `openapi: 3.1.0` makes every 3.1-aware client generator (hey-api,
// openapi-typescript, openapi-generator >=7, Swagger UI 3.1) silently
// drop the null union — `bio string @nullable` then types as `string`
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
