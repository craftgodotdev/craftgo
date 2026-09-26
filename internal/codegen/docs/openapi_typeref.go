package docs

import (
	"github.com/getkin/kin-openapi/openapi3"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

func schemaForTypeRef(t *ast.TypeRef, pkg *semantic.Package, registry *genericRegistry) *openapi3.SchemaRef {
	if t == nil {
		return &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"object"}}}
	}
	if t.Array {
		// One bracket per level (`Tag[][]` nests two arrays); the element
		// drops the `?`, since `Tag[]?` is an optional slice.
		inner := t.ElemTypeRef()
		return &openapi3.SchemaRef{Value: &openapi3.Schema{
			Type:  &openapi3.Types{"array"},
			Items: schemaForTypeRef(inner, pkg, registry),
		}}
	}
	if t.Map != nil {
		s := &openapi3.Schema{
			Type:                 &openapi3.Types{"object"},
			AdditionalProperties: openapi3.AdditionalProperties{Schema: schemaForTypeRef(t.Map.Value, pkg, registry)},
		}
		// kin-openapi has no `propertyNames` field; Extensions marshal as
		// plain keywords.
		if pn := propertyNamesForMapKey(t.Map.Key, pkg); pn != nil {
			if s.Extensions == nil {
				s.Extensions = make(map[string]any)
			}
			s.Extensions["propertyNames"] = pn
		}
		return &openapi3.SchemaRef{Value: s}
	}
	if t.Named != nil {
		if prim := primitiveSchema(t.Named.Name.String()); prim != nil {
			// An optional primitive value (`map<K, int?>`) may be null.
			if t.Optional {
				applyNullable(prim)
			}
			return &openapi3.SchemaRef{Value: prim}
		}
		name := registry.refName(t.Named)
		if t.Optional {
			return nullableRef(name)
		}
		return &openapi3.SchemaRef{Ref: "#/components/schemas/" + name}
	}
	return &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"object"}}}
}

// nullSchemaRef is `{type: "null"}`: the null branch of a nullable anyOf, or
// the `not` of a never-null guard.
func nullSchemaRef() *openapi3.SchemaRef {
	return &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"null"}}}
}

// nullableRef is the 3.1 "ref or null" schema, `anyOf: [{$ref}, {type: null}]`:
// 3.1 has no `nullable`, and a bare $ref takes no sibling keyword.
func nullableRef(refName string) *openapi3.SchemaRef {
	return &openapi3.SchemaRef{Value: &openapi3.Schema{
		AnyOf: openapi3.SchemaRefs{
			{Ref: "#/components/schemas/" + refName},
			nullSchemaRef(),
		},
	}}
}

// propertyNamesForMapKey returns the `propertyNames` schema of a map key: an
// enum's wire values or a string scalar's constraints; nil for any other key.
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
		values := enumWireStrings(ed)
		out := make([]any, len(values))
		for i, v := range values {
			out[i] = v
		}
		return &openapi3.Schema{
			Type: &openapi3.Types{"string"},
			Enum: out,
		}
	}
	if sc, ok := pkg.Scalars[name]; ok && sc != nil && sc.Primitive == "string" {
		// A numeric scalar's bounds have no form that holds for a string key.
		base := &openapi3.Schema{Type: &openapi3.Types{"string"}}
		applyConstraintFamilies(sc.Decorators, base, semantic.ConstraintLength|semantic.ConstraintText, sc.Primitive)
		return base
	}
	return nil
}

// enumWireStrings returns ed's wire values as strings, the form a map key or
// a URL takes: an int enum lists "1", "5", ...
func enumWireStrings(ed *ast.EnumDecl) []string {
	values := ed.EnumValues()
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = semantic.EnumMemberWireString(v)
	}
	return out
}

// instantiateGeneric builds the schema of decl with args substituted for its
// type parameters.
func instantiateGeneric(decl *ast.TypeDecl, args []*ast.TypeRef, pkg *semantic.Package, registry *genericRegistry) *openapi3.Schema {
	subst := semantic.SubstMap(decl.TypeParams, args)
	return schemaFromTypeDecl(decl, subst, pkg, registry)
}

// primitiveSchema returns the schema of a built-in type, or nil for any other
// name. An unsigned type gets `minimum: 0`.
func primitiveSchema(name string) *openapi3.Schema {
	sp, ok := prims.Lookup(name)
	if !ok {
		return nil
	}
	if sp.OASType == "" {
		return &openapi3.Schema{}
	}
	s := &openapi3.Schema{Type: &openapi3.Types{sp.OASType}, Format: sp.OASFormat}
	if sp.Kind == prims.Uint {
		zero := 0.0
		s.Min = &zero
	}
	return s
}
