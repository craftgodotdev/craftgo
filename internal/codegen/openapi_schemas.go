// Top-level component schemas: types, enums, scalars, errors.
package codegen

import (
	"fmt"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/errcat"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

func addSchemas(doc *openapi3.T, pkg *semantic.Package, registry *genericRegistry, names *schemaNames) {
	addTypeSchemas(doc, pkg, registry, names)
	addEnumSchemas(doc, pkg, names)
	addScalarSchemas(doc, pkg, names)
	addErrorSchemas(doc, pkg, registry, names)
}

// addErrorSchemas emits one components.schemas entry per ErrorDecl so
// `@errors(Name)` references on methods can $ref a stable target. The
// shape mirrors the wire JSON the runtime emits: `code` (string),
// `message` (string), plus any user-declared custom field. The
// resulting schema name uses the smart-suffix rule (`UserNotFound` →
// `UserNotFoundErr`), matching the Go type name in errors.go.
func addErrorSchemas(doc *openapi3.T, pkg *semantic.Package, registry *genericRegistry, names *schemaNames) {
	for _, name := range sortedKeys(pkg.Errors) {
		ed := pkg.Errors[name]
		typeName := errSuffix(ed.Name)
		s := &openapi3.Schema{
			Type:       &openapi3.Types{"object"},
			Properties: openapi3.Schemas{},
			Description: fmt.Sprintf("%s error response (HTTP %d).",
				ed.Category, errcat.Status(ed.Category)),
		}
		// A user-declared `code` / `message` body field is an exported Go
		// field that the error struct marshals and the validator enforces
		// (errorCustomFields keeps them), so it belongs in the schema like
		// any other property. Fields tagged `@header` / `@cookie` ride on
		// the response writer (see [renderErrorResponseHeadersMethod]) and
		// `@sensitive` fields are server-only, so both are excluded.
		var mixinRefs openapi3.SchemaRefs
		for _, m := range ed.Body {
			switch v := m.(type) {
			case *ast.Field:
				rf := resolveField(v, pkg, nil)
				// Same OnWireBody decision the type-schema walk uses, so error
				// and entity schemas agree on which fields ride the body (a
				// @header/@cookie field rides the response writer, a @sensitive
				// field is server-only).
				if !rf.OnWireBody {
					continue
				}
				// Carry the field's own metadata - field-level constraints
				// (@gte/@maxLength/…), @default, @deprecated, and @nullable -
				// onto the property, exactly as the type-schema walk does.
				// Without this a client consuming the error response sees
				// every field as a bare, unconstrained, non-null value.
				ref := schemaForTypeRef(v.Type, pkg, registry)
				applyFieldMetadata(v, ref, pkg)
				s.Properties[v.Name] = ref
				// Non-optional error fields belong in required[] - same
				// model as type schemas. Without this a generated client
				// types every error field as optional even though the
				// runtime always emits it.
				if rf.SpecRequired {
					s.Required = append(s.Required, v.Name)
				}
			case *ast.Mixin:
				// Embedded mixin: same `allOf: [$ref]` shape that
				// `schemaForType` uses for TypeDecl, so error schemas
				// stay consistent with type schemas when a shared
				// mixin (`Timestamps`, audit fields, ...) is embedded.
				if v == nil || v.Ref == nil || v.Ref.Name == nil {
					continue
				}
				mixinRefs = append(mixinRefs, &openapi3.SchemaRef{
					Ref: "#/components/schemas/" + mixinRefName(v.Ref, pkg, registry),
				})
			}
		}
		// A bodyless error (no declared fields) or a header/cookie-only
		// error marshals its body to `{}`. The framework's server.WriteError
		// detects that empty marshal and substitutes a `{code, message}`
		// envelope, so advertise the same shape - otherwise the spec
		// promises an empty object the server never actually sends.
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

// mixinRefName returns the component name an embedded mixin $refs. A
// generic-instance mixin (`Page<Item>`) registers and refs its
// monomorphised component (`PageOfItem`) - the same one a field of that
// type would produce - instead of the bare, never-emitted generic decl
// name. A plain mixin refs its own name.
func mixinRefName(ref *ast.NamedTypeRef, pkg *semantic.Package, registry *genericRegistry) string {
	name := ref.Name.String()
	if len(ref.Args) > 0 && pkg != nil && registry != nil {
		if decl, ok := pkg.Types[name]; ok && len(decl.TypeParams) > 0 {
			return registry.register(decl, ref.Args)
		}
	}
	return name
}

// addTypeSchemas emits one schema per concrete (non-generic) TypeDecl.
func addTypeSchemas(doc *openapi3.T, pkg *semantic.Package, registry *genericRegistry, names *schemaNames) {
	for _, name := range sortedKeys(pkg.Types) {
		td := pkg.Types[name]
		if len(td.TypeParams) > 0 {
			continue
		}
		names.put(doc, name, &openapi3.SchemaRef{Value: schemaForType(td, pkg, registry)})
	}
}

// addEnumSchemas emits one schema per EnumDecl. The schema's base type
// is `string` for bare and string-valued enums, `integer` for int-valued.
// The OpenAPI `enum` array enumerates the wire values: bare values use
// the value name, string values use the literal, int values use the
// integer.
func addEnumSchemas(doc *openapi3.T, pkg *semantic.Package, names *schemaNames) {
	for _, name := range sortedKeys(pkg.Enums) {
		ed := pkg.Enums[name]
		s := &openapi3.Schema{Type: &openapi3.Types{"string"}}
		if firstEnumKind(ed) == ast.EnumInt {
			s.Type = &openapi3.Types{"integer"}
		}
		enumVals := ed.EnumValues()
		s.Enum = make([]any, 0, len(enumVals))
		for _, v := range enumVals {
			s.Enum = append(s.Enum, enumMemberWire(v))
		}
		s.Description = resolveDescription(ed.Decorators, ed.Doc)
		names.put(doc, name, &openapi3.SchemaRef{Value: s})
	}
}

// addScalarSchemas emits one schema per ScalarDecl. The schema is
// the underlying primitive enriched with every decorator the scalar
// carries so OpenAPI consumers see the full contract:
//
//   - `@format(email)` → `format: email`
//   - `@length(1, 80)` / `@minLength(1)` / `@maxLength(80)` → minLength/maxLength
//   - `@pattern("...")` → pattern
//   - `@gte(0)` / `@lte(100)` / `@gt` / `@lt` / `@range(lo, hi)` → minimum/maximum (+exclusiveMin/Max)
//   - `@positive` / `@negative` → strict bound at 0
//   - `@multipleOf(N)` → multipleOf
//
// Carrying these keeps the spec from collapsing every scalar back to
// its bare primitive, so generated TS clients see the same
// `string` / `number` constraints the runtime validator enforces
// rather than values the server would reject.
func addScalarSchemas(doc *openapi3.T, pkg *semantic.Package, names *schemaNames) {
	for _, name := range sortedKeys(pkg.Scalars) {
		sc := pkg.Scalars[name]
		base := primitiveSchema(sc.Primitive)
		if base == nil {
			base = &openapi3.Schema{Type: &openapi3.Types{"string"}}
		}
		applyFieldConstraints(sc.Decorators, base)
		base.Description = resolveDescription(sc.Decorators, sc.Doc)
		names.put(doc, name, &openapi3.SchemaRef{Value: base})
	}
}

// schemaForType builds the openapi3.Schema for one top-level TypeDecl.
// It is the no-substitution entry point onto [schemaFromTypeDecl]; the
// generic-instance path ([instantiateGeneric]) shares the exact same
// body-walk with a populated substitution map.
//
// `@deprecated` propagates to OpenAPI in two places: type-level marks
// the entire schema as deprecated; field-level marks only that
// property. Tools like Swagger UI render deprecated entries with a
// strikethrough so consumers can spot them at a glance.
func schemaForType(td *ast.TypeDecl, pkg *semantic.Package, registry *genericRegistry) *openapi3.Schema {
	return schemaFromTypeDecl(td, nil, pkg, registry)
}

// schemaFromTypeDecl walks a TypeDecl body into an object schema. When
// subst is nil the field types are emitted verbatim (the top-level
// [schemaForType] case); when subst maps each of the decl's type-params
// to a concrete argument every field type is run through
// [substituteTypeRef] first (the concrete generic-instance case driven
// by [instantiateGeneric]). Routing both through one walk guarantees a
// `Page<Order>` instance inherits the SAME field-level metadata
// (@gte/@default/@format/@example/@nullable/@deprecated…), type-level
// description, @deprecated flag, @header/@cookie exclusion, mixin
// allOf-flattening, and @requiresOneOf/@mutuallyExclusive fragments that
// a non-generic type of the same shape carries - otherwise generic
// instances silently ship to clients as unconstrained objects.
func schemaFromTypeDecl(td *ast.TypeDecl, subst map[string]*ast.TypeRef, pkg *semantic.Package, registry *genericRegistry) *openapi3.Schema {
	s := &openapi3.Schema{
		Type:       &openapi3.Types{"object"},
		Properties: openapi3.Schemas{},
	}
	s.Description = resolveDescription(td.Decorators, td.Doc)
	if hasDeprecatedDecorator(td.Decorators) {
		s.Deprecated = true
	}
	var mixinRefs openapi3.SchemaRefs
	for _, m := range td.Body {
		switch v := m.(type) {
		case *ast.Field:
			rf := resolveField(v, pkg, nil)
			// Wire-bound (`@path`/`@query`/`@header`/`@cookie`) and
			// `@sensitive` fields carry `json:"-"` and never appear in the
			// JSON body - OnWireBody is the resolved decision (same one the
			// struct/binder use), so this can't drift from them.
			if !rf.OnWireBody {
				continue
			}
			ft := v.Type
			if subst != nil {
				ft = substituteTypeRef(v.Type, subst)
			}
			ref := schemaForTypeRef(ft, pkg, registry)
			applyFieldMetadata(v, ref, pkg)
			s.Properties[v.Name] = ref
			if rf.SpecRequired {
				s.Required = append(s.Required, v.Name)
			}
		case *ast.Mixin:
			// Embedded mixin: OpenAPI 3.0 expresses Go's field-
			// promotion via `allOf: [$ref]` so the host schema
			// inherits every property of the referenced type.
			// Skipping the mixin would leave generated TS clients
			// without the embedded fields (`createdAt`/`updatedAt`,
			// ...) and runtime requests carrying them would fail
			// spec validation.
			if v == nil || v.Ref == nil || v.Ref.Name == nil {
				continue
			}
			ref := v.Ref
			// A generic host (`Box<T>` embedding `Tree<T>`) instantiated
			// as `Box<Leaf>` must substitute its type-params into the
			// mixin's own generic args, or mixinRefName registers a
			// phantom `TreeOfT` whose element `$ref` dangles at `T`. The
			// sibling Field branch already substitutes via substituteTypeRef.
			if subst != nil && len(ref.Args) > 0 {
				cp := *ref
				cp.Args = make([]*ast.TypeRef, len(ref.Args))
				for i, a := range ref.Args {
					cp.Args[i] = substituteTypeRef(a, subst)
				}
				ref = &cp
			}
			mixinRefs = append(mixinRefs, &openapi3.SchemaRef{
				Ref: "#/components/schemas/" + mixinRefName(ref, pkg, registry),
			})
		}
	}
	// Cross-field type-level constraints render as schema-level
	// `anyOf` (`@requiresOneOf`) and `not.required` (`@mutuallyExclusive`)
	// fragments. These complement the runtime validator (which fires
	// inside Validate()) by making the same contract visible to spec-
	// driven consumers - generated TS / Java SDKs, Swagger UI, schema
	// fuzzers. Without this emit, the API doc claims every listed
	// field is independent but the server quietly rejects "all-absent"
	// or "both-present" payloads.
	crossFragments := crossFieldSchemaFragments(td.Decorators)

	// Apply allOf with the mixin refs PLUS the host's own properties
	// when at least one mixin contributed. Without mixins we keep the
	// flat object shape so simple cases stay readable in YAML output.
	if len(mixinRefs) > 0 {
		wrapAllOfWithHost(s, mixinRefs, crossFragments)
		return s
	}
	if len(crossFragments) > 0 {
		// No mixin to host an allOf wrapper; promote the existing
		// properties into one and append the cross-field fragments
		// so the schema reads as "host shape AND constraint AND
		// constraint…". This keeps the per-property metadata
		// (descriptions, formats) intact instead of inlining them
		// at the allOf level where it would lose the host's `type:
		// object` marker.
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

// crossFieldSchemaFragments returns one schema-level fragment per
// `@requiresOneOf` / `@mutuallyExclusive` on the type, ready to drop
// into an `allOf` chain. Empty result means no cross-field
// constraints - the caller keeps the flat object shape.
//
// Encoding:
//
//	@requiresOneOf(a, b, c) → `anyOf: [{required:[a]}, {required:[b]}, {required:[c]}]`
//	    JSON Schema `anyOf` requires ≥1 branch to match; each branch
//	    asserts ONE listed field is present → at least one of the
//	    listed fields must be present.
//
//	@mutuallyExclusive(a, b) → `not: { required: [a, b] }`
//	    JSON Schema `required` is conjunctive: `required:[a,b]` =
//	    "both must be present". Negating → "must NOT have BOTH" =
//	    at most one of a/b present.
//
// Both decorators may appear together on the same type (e.g. "at
// least one of these AND no two of these"); each fragment lands
// independently in the allOf chain so the constraints compose.
func crossFieldSchemaFragments(decs []*ast.Decorator) openapi3.SchemaRefs {
	var out openapi3.SchemaRefs
	for _, d := range decs {
		switch d.Name {
		case "requiresOneOf":
			names := dedupeStrings(stringArrayDecoratorArg(d))
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
			names := dedupeStrings(stringArrayDecoratorArg(d))
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

// presentNonNull builds a schema that matches a body where every named
// field is present AND not JSON null - the exact meaning the runtime
// cross-field check uses (a pointer field is "present" only when `!= nil`,
// so an explicit `null` does NOT count). Plain JSON-Schema `required` is
// key-presence only and would treat `{"x": null}` as present, diverging
// from the validator; the `properties: {x: {not: {type: null}}}` clause
// closes that gap.
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

// wrapAllOfWithHost folds a schema's own properties into an allOf alongside the
// embedded mixin refs (and any extra fragments such as cross-field
// constraints). The host's properties become one allOf member, so a
// mixin-embedding type or error renders as
// `allOf: [<mixin refs>, {host props}, <extra>]`. Mutates s in place: clears
// its Properties / Required and sets AllOf. No-op when no mixin contributed
// (a flat object shape stays readable in YAML). Shared by the type-schema and
// error-schema emitters so the two can't drift on the wrapping shape.
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
