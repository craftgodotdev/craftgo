package docs

import (
	"math/big"

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
// enum's wire values, a string scalar's constraints, or for an integer key,
// a scalar's too, the decimal digits it decodes from, signed unless the
// integer is unsigned; nil for a string key.
func propertyNamesForMapKey(t *ast.TypeRef, pkg *semantic.Package) *openapi3.Schema {
	if t == nil || t.Named == nil || t.Named.Name == nil {
		return nil
	}
	prim := t.Named.Name.String()
	if pkg != nil {
		if ed := pkg.Enums[prim]; ed != nil {
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
		if sc := pkg.Scalars[prim]; sc != nil {
			if sc.Primitive == "string" {
				base := &openapi3.Schema{Type: &openapi3.Types{"string"}}
				applyConstraintFamilies(sc.Decorators, base, semantic.ConstraintLength|semantic.ConstraintText, sc.Primitive)
				return base
			}
			// A numeric scalar's bounds have no form that holds for a string key.
			prim = sc.Primitive
		}
	}
	switch sp, _ := prims.Lookup(prim); sp.Kind {
	case prims.Int:
		return &openapi3.Schema{Type: &openapi3.Types{"string"}, Pattern: `^[+-]?[0-9]+$`}
	case prims.Uint:
		return &openapi3.Schema{Type: &openapi3.Types{"string"}, Pattern: `^[0-9]+$`}
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
// name. An integer type narrower than 64 bits or unsigned carries its range,
// and a float32 the values that parse into one: the decoder refuses the rest.
func primitiveSchema(name string) *openapi3.Schema {
	sp, ok := prims.Lookup(name)
	if !ok {
		return nil
	}
	if sp.OASType == "" {
		return &openapi3.Schema{}
	}
	s := &openapi3.Schema{Type: &openapi3.Types{sp.OASType}, Format: sp.OASFormat}
	switch {
	case sp.Kind == prims.Uint || sp.Kind == prims.Int && sp.Bits > 0 && sp.Bits < 64:
		lo, hi, _ := prims.Capacity(name)
		setNumber(s, "minimum", new(big.Rat).SetInt(lo))
		setNumber(s, "maximum", new(big.Rat).SetInt(hi))
	case sp.Kind == prims.Float && sp.Bits == 32:
		setNumber(s, "exclusiveMinimum", new(big.Rat).Neg(float32Overflow))
		setNumber(s, "exclusiveMaximum", float32Overflow)
	}
	return s
}

// float32Overflow is the least magnitude that parses past a float32's range,
// 2^128 - 2^103: halfway between the greatest float32 and 2^128, it rounds to
// 2^128, the even of the two.
var float32Overflow = new(big.Rat).SetInt(new(big.Int).Sub(
	new(big.Int).Lsh(big.NewInt(1), 128), new(big.Int).Lsh(big.NewInt(1), 103)))
