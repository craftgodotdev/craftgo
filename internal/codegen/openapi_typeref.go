// TypeRef -> SchemaRef conversion + generic instantiation.
package codegen

import (
	"github.com/getkin/kin-openapi/openapi3"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

func schemaForTypeRef(t *ast.TypeRef, pkg *semantic.Package, registry *genericRegistry) *openapi3.SchemaRef {
	if t == nil {
		return &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"object"}}}
	}
	if t.Array {
		// Peel ONE bracket per recursion so multi-array types
		// (`Tag[][]`) emit nested OpenAPI `array` schemas. The
		// inner schemaForTypeRef call sees `Tag[]`, then `Tag`.
		// Clear Optional on the inner — `Tag[]?` means "the slice
		// may be absent", not "each element may be null"; leaving
		// the flag set would propagate `nullable: true` into the
		// items schema.
		inner := *t
		inner.Optional = false
		if inner.ArrayDepth > 0 {
			inner.ArrayDepth--
		}
		if inner.ArrayDepth == 0 {
			inner.Array = false
		}
		return &openapi3.SchemaRef{Value: &openapi3.Schema{
			Type:  &openapi3.Types{"array"},
			Items: schemaForTypeRef(&inner, pkg, registry),
		}}
	}
	if t.Map != nil {
		s := &openapi3.Schema{
			Type:                 &openapi3.Types{"object"},
			AdditionalProperties: openapi3.AdditionalProperties{Schema: schemaForTypeRef(t.Map.Value, pkg, registry)},
		}
		// OpenAPI 3.1's `propertyNames` constrains the object keys.
		// JSON keys are always strings on the wire, so plain
		// string keys carry no extra constraint — but an enum key
		// implies a closed value-set and a scalar key carries the
		// scalar's own validators (length / pattern / format). Without
		// this emit `map<Color, V>` and `map<EmailID, V>` flatten to
		// untyped string keys and the generated TS / Java client SDK
		// accepts garbage keys.
		//
		// kin-openapi v0.124 doesn't model `propertyNames` natively
		// (it's primarily an OpenAPI 3.0 library; craftgo emits the
		// 3.1 version string and uses 3.1-only fields via Extensions).
		// The Extensions map marshals as top-level YAML keys so the
		// rendered output reads identically to a native field.
		if pn := propertyNamesForMapKey(t.Map.Key, pkg); pn != nil {
			if s.Extensions == nil {
				s.Extensions = make(map[string]interface{})
			}
			s.Extensions["propertyNames"] = pn
		}
		return &openapi3.SchemaRef{Value: s}
	}
	if t.Named != nil {
		name := t.Named.Name.String()
		if prim := primitiveSchema(name); prim != nil {
			return &openapi3.SchemaRef{Value: prim}
		}
		if len(t.Named.Args) > 0 {
			if generic, ok := pkg.Types[name]; ok && len(generic.TypeParams) > 0 {
				if registry != nil {
					componentName := registry.register(generic, t.Named.Args)
					// Optional generic instance keeps the same allOf
					// wrapper trick as plain named refs - bare `$ref`
					// has no `nullable` companion in OpenAPI 3.0.
					if t.Optional {
						return &openapi3.SchemaRef{Value: &openapi3.Schema{
							AllOf:    openapi3.SchemaRefs{{Ref: "#/components/schemas/" + componentName}},
							Nullable: true,
						}}
					}
					return &openapi3.SchemaRef{Ref: "#/components/schemas/" + componentName}
				}
				// No registry: fall through to legacy inline form.
				// Only the legacy unit-test path hits this branch.
				return &openapi3.SchemaRef{Value: instantiateGeneric(generic, t.Named.Args, pkg, nil)}
			}
		}
		// Bare `$ref` doesn't carry the nullable flag; the OpenAPI 3.0
		// idiom for "ref OR null" is `allOf: [$ref] + nullable: true`.
		// Without this wrapper, optional struct fields (`boss User?`)
		// emit a plain `$ref` and TS client generators type the field
		// as required `User`, refusing `null` even though the server
		// accepts it.
		if t.Optional {
			return &openapi3.SchemaRef{Value: &openapi3.Schema{
				AllOf:    openapi3.SchemaRefs{{Ref: "#/components/schemas/" + name}},
				Nullable: true,
			}}
		}
		return &openapi3.SchemaRef{Ref: "#/components/schemas/" + name}
	}
	return &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"object"}}}
}

// propertyNamesForMapKey returns the OpenAPI 3.1 `propertyNames`
// schema constraint for a map key TypeRef, or nil when the key
// carries no constraint beyond "must be a JSON string".
//
// Coverage:
//   - enum key  → `enum: [values...]` (closed set)
//   - scalar key with string primitive → inherits scalar's
//     `minLength` / `maxLength` / `pattern` / `format`
//   - bare `string` key → nil (no extra constraint)
//   - non-string scalar key → nil (the wire serialisation would
//     stringify, but expressing the underlying numeric constraint
//     via propertyNames is unsupported by every common client SDK
//     generator — emitting nothing is safer than emitting a
//     misleading constraint)
//
// Resolves through the merged package (OpenAPI generation runs after
// [mergeProjectForOpenAPI], which rewrites cross-package qualified
// refs to bare names) so cross-pkg keys land here without a
// project-resolver detour.
func propertyNamesForMapKey(t *ast.TypeRef, pkg *semantic.Package) *openapi3.Schema {
	if t == nil || t.Named == nil || t.Named.Name == nil {
		return nil
	}
	name := t.Named.Name.String()
	if name == "string" {
		return nil
	}
	if pkg == nil {
		return nil
	}
	if ed, ok := pkg.Enums[name]; ok && ed != nil {
		values := ed.EnumValues()
		out := make([]any, 0, len(values))
		for _, v := range values {
			out = append(out, v.Name)
		}
		return &openapi3.Schema{
			Type: &openapi3.Types{"string"},
			Enum: out,
		}
	}
	if sc, ok := pkg.Scalars[name]; ok && sc != nil && sc.Primitive == "string" {
		// Inherit the scalar's own string constraints — length /
		// pattern / format — so the map key constraint mirrors what
		// a bare-field of the same scalar type would receive in its
		// schema. Decorators not relevant to keys (`@gte`, `@minItems`)
		// are filtered by the underlying string-only emit.
		s := &openapi3.Schema{Type: &openapi3.Types{"string"}}
		applyScalarStringDecorators(s, sc.Decorators)
		return s
	}
	return nil
}

// applyScalarStringDecorators copies the string-shape constraints a
// scalar declares (`@minLength`, `@maxLength`, `@pattern`, `@format`)
// onto an OpenAPI schema. Centralised so the map-key `propertyNames`
// path and the existing scalar-component emit agree on which
// decorators are key-applicable.
func applyScalarStringDecorators(s *openapi3.Schema, decs []*ast.Decorator) {
	for _, d := range decs {
		if d == nil || len(d.Args) == 0 {
			continue
		}
		switch d.Name {
		case "minLength":
			if n, ok := intArgValue(d.Args[0]); ok {
				v := uint64(n)
				s.MinLength = v
			}
		case "maxLength":
			if n, ok := intArgValue(d.Args[0]); ok {
				v := uint64(n)
				s.MaxLength = &v
			}
		case "length":
			// `@length(min, max)` — two-arg form.
			if len(d.Args) == 2 {
				if mn, ok := intArgValue(d.Args[0]); ok {
					s.MinLength = uint64(mn)
				}
				if mx, ok := intArgValue(d.Args[1]); ok {
					v := uint64(mx)
					s.MaxLength = &v
				}
			} else if n, ok := intArgValue(d.Args[0]); ok {
				// Single-arg `@length(N)` — exact length, fold into
				// both bounds.
				v := uint64(n)
				s.MinLength = v
				s.MaxLength = &v
			}
		case "pattern":
			if str, ok := stringArgValue(d.Args[0]); ok {
				s.Pattern = str
			}
		case "format":
			if str, ok := stringArgValue(d.Args[0]); ok {
				s.Format = str
			}
		}
	}
}

// intArgValue extracts the int64 value from a decorator argument when
// the expression is an [ast.IntLit]. Returns (0, false) otherwise.
func intArgValue(a *ast.DecoratorArg) (int64, bool) {
	if a == nil {
		return 0, false
	}
	if lit, ok := a.Value.(*ast.IntLit); ok {
		return lit.Value, true
	}
	return 0, false
}

// stringArgValue extracts the string value from a decorator argument
// when the expression is an [ast.StringLit] or an [ast.IdentExpr]
// (some decorators accept enum-ident shortcuts for format names).
func stringArgValue(a *ast.DecoratorArg) (string, bool) {
	if a == nil {
		return "", false
	}
	switch v := a.Value.(type) {
	case *ast.StringLit:
		return v.Value, true
	case *ast.IdentExpr:
		if v.Name != nil && len(v.Name.Parts) > 0 {
			return v.Name.Parts[len(v.Name.Parts)-1], true
		}
	}
	return "", false
}

// instantiateGeneric builds the schema body for one generic instance
// (`Page<Order>`, `Result<User, Error>`, ...) by substituting each
// type-param name with the matching concrete arg and walking the
// decl's body fields + embedded mixins.
//
// Mixin expansion mirrors [schemaForType]: when the body has at least
// one mixin reference, the host schema flips to an `allOf` composition
// whose first entries are `$ref`s to each mixin's component and whose
// last entry is an inline object carrying the host's own (substituted)
// fields. Without this expansion, mixin members would be silently
// dropped during instantiation - a `Page<Order>` whose body mixed in
// `AuditFields` would land on the wire missing the audit timestamps
// it inherited at the DSL level.
//
// The registry is passed through so any nested generic encountered
// during substitution (e.g. `Page<Envelope<Order>>` recurses into
// `Envelope<Order>`) registers transitively. A nil registry falls
// back to inline emission for the nested level - kept for the no-
// registry test path that does not exercise nesting.
func instantiateGeneric(decl *ast.TypeDecl, args []*ast.TypeRef, pkg *semantic.Package, registry *genericRegistry) *openapi3.Schema {
	subst := map[string]*ast.TypeRef{}
	for i, p := range decl.TypeParams {
		if i < len(args) {
			subst[p] = args[i]
		}
	}
	s := &openapi3.Schema{
		Type:       &openapi3.Types{"object"},
		Properties: openapi3.Schemas{},
	}
	var mixinRefs openapi3.SchemaRefs
	for _, m := range decl.Body {
		switch v := m.(type) {
		case *ast.Field:
			if hasSensitiveDecorator(v.Decorators) {
				continue
			}
			s.Properties[v.Name] = schemaForTypeRef(substituteTypeRef(v.Type, subst), pkg, registry)
			if fieldIsRequired(v) {
				s.Required = append(s.Required, v.Name)
			}
		case *ast.Mixin:
			// Embedded mixin inside a generic body. The mixin name
			// itself does not undergo substitution: only TypeRef args
			// (i.e. the `T` in `items: T[]`) participate in substitution.
			// A mixin named after a type-param (`type X<Y> { Y }`) is
			// disallowed at the DSL level - mixins are always
			// PascalCase identifiers resolved against the package's
			// type table.
			if v == nil || v.Ref == nil || v.Ref.Name == nil {
				continue
			}
			mixinRefs = append(mixinRefs, &openapi3.SchemaRef{
				Ref: "#/components/schemas/" + v.Ref.Name.String(),
			})
		}
	}
	if len(mixinRefs) > 0 {
		host := &openapi3.Schema{
			Type:       &openapi3.Types{"object"},
			Properties: s.Properties,
			Required:   s.Required,
		}
		mixinRefs = append(mixinRefs, &openapi3.SchemaRef{Value: host})
		s.Properties = nil
		s.Required = nil
		s.AllOf = mixinRefs
	}
	return s
}

// substituteTypeRef walks t and swaps every NamedTypeRef whose Name is
// a known type-param key with the matching concrete TypeRef. Array and
// Optional suffixes from the original survive; the substituted ref's
// own suffixes are merged in too (so `T?` substituted with `Book[]`
// correctly produces `Book[]?`).
func substituteTypeRef(t *ast.TypeRef, subst map[string]*ast.TypeRef) *ast.TypeRef {
	if t == nil {
		return nil
	}
	if t.Map != nil {
		return &ast.TypeRef{
			Pos: t.Pos,
			Map: &ast.MapType{
				Pos:   t.Map.Pos,
				Key:   substituteTypeRef(t.Map.Key, subst),
				Value: substituteTypeRef(t.Map.Value, subst),
			},
			Array:      t.Array,
			ArrayDepth: t.ArrayDepth,
			Optional:   t.Optional,
		}
	}
	if t.Named != nil {
		if rep, ok := subst[t.Named.Name.String()]; ok {
			out := *rep
			if t.Array {
				out.Array = true
				// Add the outer's array dim count on top of any
				// the substituted ref carried (e.g. `T?` →
				// `Book[]` becomes `Book[]?` with depth=1).
				if t.ArrayDepth > 0 {
					out.ArrayDepth += t.ArrayDepth
				} else if out.ArrayDepth == 0 {
					out.ArrayDepth = 1
				}
			}
			if t.Optional {
				out.Optional = true
			}
			return &out
		}
		// The Named ref itself is not a type-param, but its generic
		// args might be: `kids: Tree<T>[]` inside `type Tree<T>` has
		// `Tree` (not a param) plus arg `T` (a param). Substitute
		// inside the args so the synthesized instance carries the
		// concrete arg, not the still-bound param. Without this the
		// post-substitution body would register the parametric
		// `Tree<T>` again at every recursive site, polluting the
		// component map with phantom `TreeOfT` entries.
		if len(t.Named.Args) > 0 {
			args := make([]*ast.TypeRef, len(t.Named.Args))
			subbed := false
			for i, a := range t.Named.Args {
				args[i] = substituteTypeRef(a, subst)
				if args[i] != a {
					subbed = true
				}
			}
			if subbed {
				cp := *t
				named := *t.Named
				named.Args = args
				cp.Named = &named
				return &cp
			}
		}
	}
	return t
}

// primitiveSchema returns an inline Schema for DSL primitive type names,
// or nil to signal "this is a user-defined type, emit a $ref".
func primitiveSchema(name string) *openapi3.Schema {
	switch name {
	case "string":
		return &openapi3.Schema{Type: &openapi3.Types{"string"}}
	case "bool":
		return &openapi3.Schema{Type: &openapi3.Types{"boolean"}}
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		return &openapi3.Schema{Type: &openapi3.Types{"integer"}}
	case "float32", "float64":
		return &openapi3.Schema{Type: &openapi3.Types{"number"}}
	case "bytes":
		return &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "byte"}
	case "file":
		return &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "binary"}
	case "any":
		return &openapi3.Schema{}
	}
	return nil
}
