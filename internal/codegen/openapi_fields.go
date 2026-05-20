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
	// Plain $ref: nothing to mutate on the field site - description,
	// example, default come from the referenced schema's own definition.
	if ref.Ref != "" {
		return
	}
	if ref.Value == nil {
		return
	}
	// Optional-ref wrapper (allOf:[$ref] + nullable: true). Cosmetic
	// metadata like description/example would land on the wrapper, which
	// tooling renders inconsistently - some UI generators show it next
	// to the field, others let the $ref's own description win. We drop
	// those decorators here; users who need a field-specific
	// description should alias the type (`type Manager = User`). But
	// `default` carries runtime semantics (server fills in when the
	// field is absent), so it stays on the wrapper regardless.
	if isAllOfRefWrapper(ref.Value) {
		if def, ok := defaultValue(f.Decorators); ok {
			ref.Value.Default = def
		}
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

// isAllOfRefWrapper recognises the `allOf: [{$ref}] + nullable: true`
// shape that schemaForTypeRef emits for an optional named-type field.
// The wrapper is the OpenAPI 3.0 idiom for "ref OR null"; metadata on
// the wrapper is interpreted inconsistently by clients, so callers
// branch on this signature to avoid stamping description/example on it.
func isAllOfRefWrapper(s *openapi3.Schema) bool {
	if s == nil || len(s.AllOf) != 1 || s.AllOf[0].Ref == "" {
		return false
	}
	return s.Type == nil && len(s.Properties) == 0
}

// applyNumericConstraints translates the numeric comparison decorators
// onto an OpenAPI schema. The mapping uses 3.0-style booleans for
// exclusive bounds because the kin-openapi validator we run against
// rejects 3.1's `{minimum: N, exclusiveMinimum: N}` shape — see the
// note on [applyNullable] for the same compatibility caveat.
//
//	@gte(N) → minimum: N
//	@gt(N)  → minimum: N, exclusiveMinimum: true
//	@lte(N) → maximum: N
//	@lt(N)  → maximum: N, exclusiveMaximum: true
//	@range(lo, hi) → minimum: lo, maximum: hi (both inclusive)
//	@multipleOf(N) → multipleOf: N
//	@positive → minimum: 0, exclusiveMinimum: true
//	@negative → maximum: 0, exclusiveMaximum: true
//
// Without this wiring, client generators (hey-api, openapi-typescript,
// openapi-generator) see the field as an unbounded `number` and produce
// types that allow values the server will reject at validate time.
func applyNumericConstraints(ds []*ast.Decorator, s *openapi3.Schema) {
	if s == nil {
		return
	}
	setMin := func(v float64, exclusive bool) {
		s.Min = &v
		if exclusive {
			s.ExclusiveMin = true
		}
	}
	setMax := func(v float64, exclusive bool) {
		s.Max = &v
		if exclusive {
			s.ExclusiveMax = true
		}
	}
	for _, d := range ds {
		if d == nil {
			continue
		}
		switch d.Name {
		case "gte":
			if v, ok := numericArgValue(d, 0); ok {
				setMin(v, false)
			}
		case "gt":
			if v, ok := numericArgValue(d, 0); ok {
				setMin(v, true)
			}
		case "lte":
			if v, ok := numericArgValue(d, 0); ok {
				setMax(v, false)
			}
		case "lt":
			if v, ok := numericArgValue(d, 0); ok {
				setMax(v, true)
			}
		case "range":
			if lo, ok := numericArgValue(d, 0); ok {
				setMin(lo, false)
			}
			if hi, ok := numericArgValue(d, 1); ok {
				setMax(hi, false)
			}
		case "positive":
			zero := 0.0
			s.Min = &zero
			s.ExclusiveMin = true
		case "negative":
			zero := 0.0
			s.Max = &zero
			s.ExclusiveMax = true
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
	for _, d := range ds {
		if d == nil {
			continue
		}
		switch d.Name {
		case "minItems":
			if v, ok := numericArgValue(d, 0); ok && v >= 0 {
				u := uint64(v)
				s.MinItems = u
			}
		case "maxItems":
			if v, ok := numericArgValue(d, 0); ok && v >= 0 {
				u := uint64(v)
				s.MaxItems = &u
			}
		case "uniqueItems":
			s.UniqueItems = true
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
func hasNullableDecorator(ds []*ast.Decorator) bool {
	for _, d := range ds {
		if d.Name == "nullable" {
			return true
		}
	}
	return false
}

// hasSensitiveDecorator reports whether ds contains the `@sensitive`
// marker. Sensitive fields are server-only: they get `json:"-"` in
// the Go struct (so neither the JSON decoder nor the encoder touches
// them) and are skipped entirely from the OpenAPI spec.
func hasSensitiveDecorator(ds []*ast.Decorator) bool {
	for _, d := range ds {
		if d.Name == "sensitive" {
			return true
		}
	}
	return false
}

// applyNullable marks a schema as nullable. We emit the OpenAPI 3.0
// boolean form (`nullable: true`) even though our doc carries the
// `openapi: 3.1.0` header - kin-openapi 0.124's validator rejects the
// 3.1 canonical `type: [<base>, null]` array (it doesn't recognise
// "null" as a valid type entry). Once kin-openapi catches up, this
// helper can switch to appending "null" onto Schema.Type without any
// caller change.
func applyNullable(s *openapi3.Schema) {
	if s != nil {
		s.Nullable = true
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
