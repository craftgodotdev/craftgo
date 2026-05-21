// Top-level component schemas: types, enums, scalars, errors.
package codegen

import (
	"fmt"
	"sort"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

func addSchemas(doc *openapi3.T, pkg *semantic.Package, registry *genericRegistry) {
	addTypeSchemas(doc, pkg, registry)
	addEnumSchemas(doc, pkg)
	addScalarSchemas(doc, pkg)
	addErrorSchemas(doc, pkg, registry)
}

// addErrorSchemas emits one components.schemas entry per ErrorDecl so
// `@errors(Name)` references on methods can $ref a stable target. The
// shape mirrors the wire JSON the runtime emits: `code` (string),
// `message` (string), plus any user-declared custom field. The
// resulting schema name uses the smart-suffix rule (`UserNotFound` →
// `UserNotFoundErr`), matching the Go type name in errors.go.
func addErrorSchemas(doc *openapi3.T, pkg *semantic.Package, registry *genericRegistry) {
	names := make([]string, 0, len(pkg.Errors))
	for n := range pkg.Errors {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		ed := pkg.Errors[name]
		typeName := errSuffix(ed.Name)
		s := &openapi3.Schema{
			Type:       &openapi3.Types{"object"},
			Properties: openapi3.Schemas{},
			Description: fmt.Sprintf("%s error response (HTTP %d).",
				ed.Category, categoryStatus[ed.Category]),
		}
		// `code` / `message` are reserved DSL slots (design-time
		// override of the framework defaults via `@default(...)`) and
		// never appear on the wire - they're internal metadata exposed
		// through the `ErrCode()` / `Error()` methods. Fields tagged with
		// `@header` / `@cookie` are also excluded - they ride on the
		// response writer (see [renderErrorResponseHeadersMethod]).
		// Anything else becomes a regular property on the schema.
		var mixinRefs openapi3.SchemaRefs
		for _, m := range ed.Body {
			switch v := m.(type) {
			case *ast.Field:
				if v.Name == "code" || v.Name == "message" {
					continue
				}
				if hasSensitiveDecorator(v.Decorators) {
					continue
				}
				switch bindingFromDecorators(v.Decorators) {
				case "header", "cookie":
					continue
				}
				s.Properties[v.Name] = schemaForTypeRef(v.Type, pkg, registry)
			case *ast.Mixin:
				// Embedded mixin: same `allOf: [$ref]` shape that
				// `schemaForType` uses for TypeDecl, so error schemas
				// stay consistent with type schemas when a shared
				// mixin (`Timestamps`, audit fields, ...) is embedded.
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
		doc.Components.Schemas[typeName] = &openapi3.SchemaRef{Value: s}
	}
}

// addTypeSchemas emits one schema per concrete (non-generic) TypeDecl.
func addTypeSchemas(doc *openapi3.T, pkg *semantic.Package, registry *genericRegistry) {
	names := make([]string, 0, len(pkg.Types))
	for n := range pkg.Types {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		td := pkg.Types[name]
		if len(td.TypeParams) > 0 {
			continue
		}
		doc.Components.Schemas[name] = &openapi3.SchemaRef{Value: schemaForType(td, pkg, registry)}
	}
}

// addEnumSchemas emits one schema per EnumDecl. The schema's base type
// is `string` for bare and string-valued enums, `integer` for int-valued.
// The OpenAPI `enum` array enumerates the wire values: bare values use
// the value name, string values use the literal, int values use the
// integer.
func addEnumSchemas(doc *openapi3.T, pkg *semantic.Package) {
	names := make([]string, 0, len(pkg.Enums))
	for n := range pkg.Enums {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		ed := pkg.Enums[name]
		s := &openapi3.Schema{Type: &openapi3.Types{"string"}}
		if firstEnumKind(ed) == ast.EnumInt {
			s.Type = &openapi3.Types{"integer"}
		}
		enumVals := ed.EnumValues()
		s.Enum = make([]any, 0, len(enumVals))
		for _, v := range enumVals {
			switch v.Kind {
			case ast.EnumInt:
				s.Enum = append(s.Enum, v.IntValue)
			case ast.EnumString:
				s.Enum = append(s.Enum, v.StrValue)
			default: // EnumBare - wire value is the source-side name
				s.Enum = append(s.Enum, v.Name)
			}
		}
		doc.Components.Schemas[name] = &openapi3.SchemaRef{Value: s}
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
// Without these the OpenAPI spec collapsed every scalar back to its
// bare primitive — the runtime validator enforced the rules but
// generated TS clients saw only `string` / `number` and could send
// values the server would reject.
func addScalarSchemas(doc *openapi3.T, pkg *semantic.Package) {
	names := make([]string, 0, len(pkg.Scalars))
	for n := range pkg.Scalars {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		sc := pkg.Scalars[name]
		base := primitiveSchema(sc.Primitive)
		if base == nil {
			base = &openapi3.Schema{Type: &openapi3.Types{"string"}}
		}
		applyPatternFormat(sc.Decorators, base)
		applyStringLengthConstraints(sc.Decorators, base)
		applyNumericConstraints(sc.Decorators, base)
		doc.Components.Schemas[name] = &openapi3.SchemaRef{Value: base}
	}
}

// schemaForType builds the openapi3.Schema for one TypeDecl. Only Field
// members are emitted; mixin expansion is a forward-looking concern.
//
// `@deprecated` propagates to OpenAPI in two places: type-level marks
// the entire schema as deprecated; field-level marks only that
// property. Tools like Swagger UI render deprecated entries with a
// strikethrough so consumers can spot them at a glance.
func schemaForType(td *ast.TypeDecl, pkg *semantic.Package, registry *genericRegistry) *openapi3.Schema {
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
			if hasSensitiveDecorator(v.Decorators) {
				continue
			}
			ref := schemaForTypeRef(v.Type, pkg, registry)
			applyFieldMetadata(v, ref)
			s.Properties[v.Name] = ref
			if fieldIsRequired(v) {
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
			mixinRefs = append(mixinRefs, &openapi3.SchemaRef{
				Ref: "#/components/schemas/" + v.Ref.Name.String(),
			})
		}
	}
	// Apply allOf with the mixin refs PLUS the host's own properties
	// when at least one mixin contributed. Without mixins we keep the
	// flat object shape so simple cases stay readable in YAML output.
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
