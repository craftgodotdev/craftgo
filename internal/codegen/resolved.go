// Resolved field/type IR: the single, fully-resolved view of a type's
// fields that every codegen stage consumes - so no stage re-walks the AST
// and re-derives a field fact (is-on-wire, is-required, is-pointer, wire
// name, default value) differently from another and drifts.
//
// This is the anti-drift layer the cross-stage audits kept pointing at:
// the same fact was computed in three-plus places (fieldIsRequired vs the
// validator's presence gate vs the wire-presence gate; isNonBodyBound vs
// the schema header/cookie skip; flattenFields vs a stage's own td.Body
// walk). [resolveFields] computes each fact ONCE, from the same helpers
// the stages used to call inline, so a consumer that reads a ResolvedField
// gets a value that cannot disagree with another consumer's.
package codegen

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// Binding is where a field's value rides on the wire, derived from its
// explicit binding decorator (auto-@path / auto-@query are method-context
// dependent and applied by the request-binding stage, not here).
type Binding int

const (
	BindBody Binding = iota // JSON request/response body (the default)
	BindPath
	BindQuery
	BindHeader
	BindCookie
	BindForm
	BindSensitive // @sensitive: server-only, json:"-", excluded everywhere
)

// String renders the OpenAPI `in` value for the wire bindings; body and
// sensitive have no `in`.
func (b Binding) String() string {
	switch b {
	case BindPath:
		return wire.BindingPath
	case BindQuery:
		return wire.BindingQuery
	case BindHeader:
		return wire.BindingHeader
	case BindCookie:
		return wire.BindingCookie
	case BindForm:
		return wire.BindingForm
	case BindSensitive:
		return wire.BindingSensitive
	default:
		return wire.BindingBody
	}
}

// ResolvedField is the resolved view of one field after mixin flattening
// and generic-argument substitution. Every value is computed from the
// canonical helper, so the field is the single source of truth a stage
// reads instead of recomputing.
type ResolvedField struct {
	// Field is the (generic-substituted) source field; stages that still
	// need raw decorators or the type ref read it from here.
	Field *ast.Field

	DSLName string // wire/json base name (the source identifier)
	GoName  string // exported Go field identifier
	GoType  string // final Go type, including any *T nullable wrap

	Binding    Binding // wire placement (after request auto-binding, if any)
	OnWireBody bool    // appears as a property in the JSON body schema/struct
	// AutoBound is true when resolveRequestFields promoted an un-decorated
	// field to @path / @query (vs an explicit binding). Stages use it to
	// distinguish an explicit binding that fails to lower (a hard error)
	// from an auto-promoted field that merely can't ride the wire (skipped
	// silently). Always false for response/explicit fields.
	AutoBound bool

	IsPointer     bool // generated Go type is *T
	NeedsNilGuard bool // a constraint check must nil-guard before len()/deref

	HasDefault  bool // carries @default
	DefaultWire any  // resolved OpenAPI default value (enum member -> wire), nil if none
	HasDefValue bool // a default value was resolved

	// SpecRequired: the field belongs in the OpenAPI required[] (not
	// optional, no @default). RuntimeEnforced: the validator emits a
	// presence check for it (not optional, not @nullable - pointer-backed
	// fields are presence-checked via nil). Stored side-by-side so the
	// schema/param/validate stages read ONE answer each, and a test can
	// assert their relationship as a single visible invariant rather than
	// an emergent property of separate predicates. They differ by design on
	// @default (excluded from SpecRequired) and @nullable (excluded from
	// RuntimeEnforced) - the test pins exactly where.
	SpecRequired    bool
	RuntimeEnforced bool
}

// WireName returns the field's wire parameter name for its explicit
// binding (the decorator's name arg, or the field name). Empty for a body
// or sensitive field.
func (rf ResolvedField) WireName() string {
	switch rf.Binding {
	case BindPath, BindQuery, BindHeader, BindCookie, BindForm:
		return bindingWireName(rf.Field, rf.Binding.String())
	default:
		return ""
	}
}

// bindingFromKind maps a binding-kind string (from [wire.BindingKind] /
// [wire.RequestFieldBinding]) to the codegen Binding enum. "body" and the
// empty string both map to BindBody.
func bindingFromKind(kind string) Binding {
	switch kind {
	case wire.BindingPath:
		return BindPath
	case wire.BindingQuery:
		return BindQuery
	case wire.BindingHeader:
		return BindHeader
	case wire.BindingCookie:
		return BindCookie
	case wire.BindingForm:
		return BindForm
	case wire.BindingSensitive:
		return BindSensitive
	default:
		return BindBody
	}
}

// explicitBinding maps a field's binding decorator (if any) to a Binding.
// @sensitive wins over a binding decorator (the semantic layer rejects the
// combination, so in valid input they never co-occur).
func explicitBinding(f *ast.Field) Binding {
	if hasSensitiveDecorator(f.Decorators) {
		return BindSensitive
	}
	return bindingFromKind(bindingFromDecorators(f.Decorators))
}

// lookupMethodType resolves a method's request / response NamedTypeRef to its
// declaration plus the package prefix its bare mixins resolve against. A
// qualified ref (`shared.Holder`) is NOT in the consumer's bare-keyed
// pkg.Types, so it falls through the project resolver and the prefix is its
// home package; a local ref resolves in pkg.Types with an empty prefix.
//
// This is the single resolution every per-request / per-response codegen pass
// shares - the field resolver, default pre-fill, import collector, and
// response-header/cookie writers - so a qualified type is never silently
// dropped by one stage (an `undefined: pkg` import, a missing pre-fill, an
// unwritten response header) while a sibling stage emits it. Returns (nil, "")
// when unresolvable; a nil resolver (the OpenAPI single-package callers) keeps
// the local-only behavior those callers had.
func lookupMethodType(ref *ast.NamedTypeRef, pkg *semantic.Package, r *ProjectResolver) (*ast.TypeDecl, string) {
	if ref == nil || ref.Name == nil {
		return nil, ""
	}
	name := ref.Name.String()
	prefix := ""
	if parts := ref.Name.Parts; len(parts) == 2 {
		prefix = parts[0]
	}
	if td, ok := pkg.Types[name]; ok {
		return td, prefix
	}
	if r != nil {
		if td := r.LookupType(name); td != nil {
			return td, prefix
		}
	}
	return nil, prefix
}

// resolveRequestFields resolves m's request-type fields with method
// context applied: an un-decorated field auto-binds to @path (its name
// matches a `{param}` segment), to @query (a body-less verb has no body to
// decode into), or stays @body (a body verb). This is the single place the
// request auto-binding rule lives - the per-stage walks used to each
// re-derive the path-segment + verb-default chain (the source of binding
// drift like the @nullable-auto-query break); they now read rf.Binding.
func resolveRequestFields(m *ast.Method, pkg *semantic.Package, r *ProjectResolver) []ResolvedField {
	if m == nil || m.Request == nil {
		return nil
	}
	td, prefix := lookupMethodType(m.Request, pkg, r)
	if td == nil {
		return nil
	}
	// Full-route path variables (the owning service's @prefix vars + the method
	// path vars), so a field matching a @prefix variable auto-binds to @path -
	// the same shared rule the analyser's binding checks read, so codegen and
	// semantics can't disagree on where the field rides.
	pathNames := semantic.MethodRoutePathVars(m, pkg.Services)
	bodyVerb := wire.IsBodyVerb(m.Verb)
	fields := resolveFieldsWithPrefix(td, prefix, pkg, r)
	for i := range fields {
		rf := &fields[i]
		// Only an un-decorated field auto-binds. explicitBinding maps both
		// `@body` and no-decorator to BindBody, so an explicit @body (raw
		// decorator non-empty) is left as body.
		if rf.Binding != BindBody || bindingFromDecorators(rf.Field.Decorators) != "" {
			continue
		}
		// The auto-binding rule lives in wire.RequestFieldBinding so the
		// analyser's binding checks and this resolver agree on where the field
		// rides.
		kind, auto := wire.RequestFieldBinding(rf.Field, pathNames, bodyVerb)
		rf.Binding = bindingFromKind(kind)
		rf.AutoBound = auto
		rf.OnWireBody = rf.Binding == BindBody
	}
	return fields
}

// resolveFields flattens td (mixins expanded, generic args substituted -
// one body walk) and resolves every field. This is the single place the
// per-field facts are computed; stages read the result instead of
// re-deriving from the AST.
func resolveFields(td *ast.TypeDecl, pkg *semantic.Package, r *ProjectResolver) []ResolvedField {
	return resolveFieldsWithPrefix(td, "", pkg, r)
}

// resolveFieldsWithPrefix is [resolveFields] with the package-prefix
// context for bare mixins in td's body (see [flattenFieldsIn]). A non-empty
// prefix is needed when td was reached through a qualified reference (a
// cross-package request type), so its bare nested mixins resolve in td's
// home package rather than being dropped.
func resolveFieldsWithPrefix(td *ast.TypeDecl, prefix string, pkg *semantic.Package, r *ProjectResolver) []ResolvedField {
	flat := flattenFieldsWithNames(td, prefix, pkg, r, map[string]bool{})
	out := make([]ResolvedField, 0, len(flat))
	for _, ff := range flat {
		rf := resolveField(ff.Field, pkg, r)
		// The dedup-resolved Go identifier from the declaring struct, so the
		// binder / default / response writer land on the same field the struct
		// declares (colliding siblings get the `_2` suffix everywhere).
		rf.GoName = ff.GoName
		out = append(out, rf)
	}
	return out
}

// resolveField resolves a SINGLE field (no mixin flattening). Used by the
// schema emitters, which keep mixins as an `allOf: [$ref]` rather than
// inlining them, but still need the same per-field facts (is-on-wire,
// is-required, default) the flattened consumers get - so the decision is
// computed once here instead of each emitter re-deriving it.
func resolveField(f *ast.Field, pkg *semantic.Package, r *ProjectResolver) ResolvedField {
	dv, hasDV := resolveDefaultValue(f, pkg)
	return ResolvedField{
		Field:         f,
		DSLName:       f.Name,
		GoName:        GoFieldName(f.Name),
		GoType:        goFieldType(f, pkg, r),
		Binding:       explicitBinding(f),
		OnWireBody:    !isNonBodyBound(f) && !hasSensitiveDecorator(f.Decorators),
		IsPointer:     goFieldIsPointer(f, pkg, r),
		NeedsNilGuard: fieldNeedsNilGuard(f, pkg, r),
		HasDefault:    ast.HasDecorator(f.Decorators, "default"),
		DefaultWire:   dv,
		HasDefValue:   hasDV,
		SpecRequired:  fieldIsRequired(f),
		// The validator's presence gate (validate_registry.go): a
		// non-optional, non-@nullable field gets a presence check. @nullable
		// opts out (an explicit null is allowed); optional opts out (absence
		// is allowed); @sensitive opts out too - it is `json:"-"` (off the
		// wire, like OnWireBody above), so a presence check on it can never be
		// satisfied and would 400 every request.
		RuntimeEnforced: f.Type != nil && !f.Type.Optional && !hasNullableDecorator(f.Decorators) && !hasSensitiveDecorator(f.Decorators),
	}
}
