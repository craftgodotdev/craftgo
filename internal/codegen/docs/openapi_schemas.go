package docs

import (
	"cmp"
	"fmt"
	"maps"
	"slices"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/errcat"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

func addSchemas(doc *openapi3.T, pkg *semantic.Package, registry *genericRegistry, names *schemaNames) {
	addTypeSchemas(doc, pkg, registry, names)
	addEnumSchemas(doc, pkg, names)
	addScalarSchemas(doc, pkg, names)
	addErrorSchemas(doc, pkg, registry, names)
}

// addErrorSchemas emits one schema per error, under its type name
// (`UserNotFound` → `UserNotFoundErr`).
func addErrorSchemas(doc *openapi3.T, pkg *semantic.Package, registry *genericRegistry, names *schemaNames) {
	for _, name := range slices.Sorted(maps.Keys(pkg.Errors)) {
		ed := pkg.Errors[name]
		typeName := idents.ErrorTypeName(ed.Name)
		s := &openapi3.Schema{
			Type:       &openapi3.Types{"object"},
			Properties: openapi3.Schemas{},
			Description: fmt.Sprintf("%s error response (HTTP %d).",
				ed.Category, errcat.Status(ed.Category)),
		}
		var mixinRefs openapi3.SchemaRefs
		for _, m := range ed.Body {
			switch v := m.(type) {
			case *ast.Field:
				addBodyProperty(s, semantic.ResolveField(v, pkg, registry.resolver.Project()), v.Type, pkg, registry)
			case *ast.Mixin:
				if v == nil || v.Ref == nil || v.Ref.Name == nil {
					continue
				}
				mixinRefs = append(mixinRefs, &openapi3.SchemaRef{
					Ref: "#/components/schemas/" + registry.refName(v.Ref),
				})
			}
		}
		// An error with no body field marshals to `{}`, which server.WriteError
		// replaces with a `{code, message}` envelope.
		if len(s.Properties) == 0 && len(mixinRefs) == 0 {
			strProp := func() *openapi3.SchemaRef {
				return &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}}
			}
			s.Properties["code"] = strProp()
			s.Properties["message"] = strProp()
			s.Required = []string{"code", "message"}
		}
		wrapAllOfWithHost(s, mixinRefs, nil)
		names.put(doc, typeName, &openapi3.SchemaRef{Value: s})
	}
}

// addTypeSchemas emits one schema per non-generic type.
func addTypeSchemas(doc *openapi3.T, pkg *semantic.Package, registry *genericRegistry, names *schemaNames) {
	for _, name := range slices.Sorted(maps.Keys(pkg.Types)) {
		td := pkg.Types[name]
		if len(td.TypeParams) > 0 {
			continue
		}
		names.put(doc, name, &openapi3.SchemaRef{Value: schemaFromTypeDecl(td, nil, pkg, registry)})
	}
}

// addEnumSchemas emits one schema per enum listing its wire values: an
// `integer` for an int enum, else a `string`.
func addEnumSchemas(doc *openapi3.T, pkg *semantic.Package, names *schemaNames) {
	for _, name := range slices.Sorted(maps.Keys(pkg.Enums)) {
		ed := pkg.Enums[name]
		s := &openapi3.Schema{Type: &openapi3.Types{"string"}}
		if semantic.EnumKind(ed) == ast.EnumInt {
			s.Type = &openapi3.Types{"integer"}
		}
		enumVals := ed.EnumValues()
		s.Enum = make([]any, 0, len(enumVals))
		for _, v := range enumVals {
			s.Enum = append(s.Enum, semantic.EnumMemberWire(v))
		}
		s.Description = semantic.Description(ed.Decorators, ed.Doc)
		names.put(doc, name, &openapi3.SchemaRef{Value: s})
	}
}

// addScalarSchemas emits one schema per scalar: its primitive with its
// constraint keywords.
func addScalarSchemas(doc *openapi3.T, pkg *semantic.Package, names *schemaNames) {
	for _, name := range slices.Sorted(maps.Keys(pkg.Scalars)) {
		sc := pkg.Scalars[name]
		base := primitiveSchema(sc.Primitive)
		if base == nil {
			base = &openapi3.Schema{Type: &openapi3.Types{"string"}}
		}
		applyFieldConstraints(sc.Decorators, base)
		if desc := semantic.Description(sc.Decorators, sc.Doc); desc != "" {
			// The doc goes ahead of the note `@format(raw)` may have set.
			base.Description = appendDescription(desc, base.Description)
		}
		names.put(doc, name, &openapi3.SchemaRef{Value: base})
	}
}

// schemaFromTypeDecl builds td's object schema, substituting subst into its
// field types; subst is nil for a non-generic type.
func schemaFromTypeDecl(td *ast.TypeDecl, subst map[string]*ast.TypeRef, pkg *semantic.Package, registry *genericRegistry) *openapi3.Schema {
	s := &openapi3.Schema{
		Type:       &openapi3.Types{"object"},
		Properties: openapi3.Schemas{},
	}
	s.Description = semantic.Description(td.Decorators, td.Doc)
	if semantic.IsDeprecated(td.Decorators) {
		s.Deprecated = true
	}
	var mixinRefs openapi3.SchemaRefs
	for _, m := range td.Body {
		switch v := m.(type) {
		case *ast.Field:
			ft := v.Type
			if subst != nil {
				ft = semantic.SubstituteTypeRef(v.Type, subst)
			}
			addBodyProperty(s, semantic.ResolveField(v, pkg, registry.resolver.Project()), ft, pkg, registry)
		case *ast.Mixin:
			if v == nil || v.Ref == nil || v.Ref.Name == nil {
				continue
			}
			ref := v.Ref
			// A generic host substitutes into the mixin's arguments too:
			// `Box<Leaf>` embedding `Tree<T>` refs `TreeOfLeaf`.
			if subst != nil && len(ref.Args) > 0 {
				cp := *ref
				cp.Args = make([]*ast.TypeRef, len(ref.Args))
				for i, a := range ref.Args {
					cp.Args[i] = semantic.SubstituteTypeRef(a, subst)
				}
				ref = &cp
			}
			mixinRefs = append(mixinRefs, &openapi3.SchemaRef{
				Ref: "#/components/schemas/" + registry.refName(ref),
			})
		}
	}
	crossFragments := typeFragments(td, registry)

	if len(mixinRefs) > 0 {
		wrapAllOfWithHost(s, mixinRefs, crossFragments)
		return s
	}
	if len(crossFragments) > 0 {
		// Without a mixin the own properties still become the first
		// allOf member, ahead of the fragments.
		host := &openapi3.Schema{
			Type:       &openapi3.Types{"object"},
			Properties: s.Properties,
			Required:   s.Required,
		}
		s.Properties = nil
		s.Required = nil
		s.AllOf = append(openapi3.SchemaRefs{{Value: host}}, crossFragments...)
	}
	return s
}

// addBodyProperty puts rf's field, typed ft, in s under its JSON name,
// required when rf.SpecRequired; a field off the body is left out.
func addBodyProperty(s *openapi3.Schema, rf semantic.ResolvedField, ft *ast.TypeRef, pkg *semantic.Package, registry *genericRegistry) {
	if !rf.OnWireBody {
		return
	}
	ref := schemaForTypeRef(ft, pkg, registry)
	applyFieldMetadata(rf.Field, ref, pkg)
	key := wire.JSONName(rf.Field)
	s.Properties[key] = ref
	if rf.SpecRequired {
		s.Required = append(s.Required, key)
	}
}

// typeFragments returns the cross-field fragments of td's own decorators,
// each member under its JSON key; the schema's mixin refs carry theirs.
func typeFragments(td *ast.TypeDecl, registry *genericRegistry) openapi3.SchemaRefs {
	return crossFieldSchemaFragments(td.Decorators, jsonKeys(td, registry))
}

// inlineFragments returns the cross-field fragments of a body listing td's
// fields in place: those of each type its mixins embed, recursively and each
// type once, then its own, every member under its keys entry.
func inlineFragments(td *ast.TypeDecl, keys map[string]string, registry *genericRegistry) openapi3.SchemaRefs {
	var decs []*ast.Decorator
	seen := map[*ast.TypeDecl]bool{}
	var walk func(*ast.TypeDecl)
	walk = func(td *ast.TypeDecl) {
		if td == nil || seen[td] {
			return
		}
		seen[td] = true
		for _, m := range td.Body {
			if mx, ok := m.(*ast.Mixin); ok && mx.Ref != nil && mx.Ref.Name != nil {
				walk(registry.resolver.LookupType(mx.Ref.Name.String()))
			}
		}
		decs = append(decs, td.Decorators...)
	}
	walk(td)
	return crossFieldSchemaFragments(decs, keys)
}

// crossFieldSchemaFragments returns `@requiresOneOf` as an `anyOf` of "x present" branches and
// `@mutuallyExclusive` as a `not` of "all present", each member under its keys entry, else its name.
func crossFieldSchemaFragments(decs []*ast.Decorator, keys map[string]string) openapi3.SchemaRefs {
	memberKeys := func(d *ast.Decorator) []string {
		names := semantic.CrossFieldNames(d)
		for i, n := range names {
			names[i] = cmp.Or(keys[n], n)
		}
		return names
	}
	var out openapi3.SchemaRefs
	for _, d := range decs {
		if d == nil {
			continue
		}
		switch d.Name {
		case "requiresOneOf":
			names := memberKeys(d)
			if len(names) == 0 {
				continue
			}
			branches := make(openapi3.SchemaRefs, 0, len(names))
			for _, n := range names {
				branches = append(branches, &openapi3.SchemaRef{Value: presentNonNull([]string{n})})
			}
			out = append(out, &openapi3.SchemaRef{Value: &openapi3.Schema{
				AnyOf: branches,
			}})
		case "mutuallyExclusive":
			names := memberKeys(d)
			if len(names) < 2 {
				continue
			}
			out = append(out, &openapi3.SchemaRef{Value: &openapi3.Schema{
				Not: &openapi3.SchemaRef{Value: presentNonNull(names)},
			}})
		}
	}
	return out
}

// jsonKeys maps the name of each field of td, mixins included, to its JSON
// key; of two fields sharing a name, the first counts.
func jsonKeys(td *ast.TypeDecl, registry *genericRegistry) map[string]string {
	keys := map[string]string{}
	for _, ff := range semantic.FlattenFields(td, "", registry.resolver, nil) {
		if _, dup := keys[ff.Field.Name]; !dup {
			keys[ff.Field.Name] = wire.JSONName(ff.Field)
		}
	}
	return keys
}

// presentNonNull matches a body with every named field present and not null,
// as the runtime counts presence; `required` alone accepts `{"x": null}`.
func presentNonNull(names []string) *openapi3.Schema {
	props := openapi3.Schemas{}
	for _, n := range names {
		props[n] = &openapi3.SchemaRef{Value: &openapi3.Schema{
			Not: nullSchemaRef(),
		}}
	}
	return &openapi3.Schema{
		Required:   append([]string(nil), names...),
		Properties: props,
	}
}

// wrapAllOfWithHost turns s into `allOf: [<mixin refs>, {s's properties},
// <extra>]`, leaving s as it is when there is no mixin ref.
func wrapAllOfWithHost(s *openapi3.Schema, mixinRefs, extra openapi3.SchemaRefs) {
	if len(mixinRefs) == 0 {
		return
	}
	host := &openapi3.Schema{
		Type:       &openapi3.Types{"object"},
		Properties: s.Properties,
		Required:   s.Required,
	}
	all := append(mixinRefs, &openapi3.SchemaRef{Value: host})
	all = append(all, extra...)
	s.Properties = nil
	s.Required = nil
	s.AllOf = all
}
