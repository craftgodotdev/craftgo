package codegen

// Scalar / enum Validate() method bodies.
//
// A constrained scalar (`scalar Email string @format(email)`) and every
// enum carry their declared checks on their OWN Validate() method rather
// than having those checks inlined at every field that uses the type.
// Scalars emit as DEFINED Go types (`type Email string`) instead of
// aliases (`type Email = string`) so they can carry a method, which an
// alias cannot. Fields then dispatch through `v.Field.Validate()` (see
// [nestedValidateCall]), and generic instances (`Page[Email]`,
// `Page[Color]`) pick the checks up through the runtime
// `interface{ Validate() error }` probe in [typeParamValidateCall],
// which only fires when the element type actually has a Validate()
// method.

import (
	"fmt"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// scalarFieldPrimitive returns the underlying primitive (DSL name) of a
// flat scalar-typed field, or "" when the field is not a scalar (or is
// an array / map, where element-level decorators don't apply). Resolves
// both local (`pkg.Scalars`) and cross-package (`resolver.LookupScalar`)
// scalar declarations.
func scalarFieldPrimitive(f *ast.Field, ctx emitCtx) string {
	if f == nil || f.Type == nil || f.Type.Array || f.Type.Map != nil || f.Type.Named == nil || f.Type.Named.Name == nil {
		return ""
	}
	name := f.Type.Named.Name.String()
	var sd *ast.ScalarDecl
	if ctx.pkg != nil {
		sd = ctx.pkg.Scalars[name]
	}
	if sd == nil && ctx.resolver != nil {
		sd = ctx.resolver.LookupScalar(name)
	}
	if sd == nil {
		return ""
	}
	return scalarPrimitiveDSL(sd.Primitive)
}

// enumFieldPrimitive returns the underlying primitive ("int" or "string")
// of a flat enum-typed field, or "" when the field is not an enum (or is
// an array / map). A field-level numeric / string constraint on an enum is
// advertised in OpenAPI (via the allOf wrapper), so it must be enforced
// too — routing through [scalarFieldLevelChecks] with this primitive casts
// the enum value to int / string before the numeric / string emitters run,
// the same trick scalars use. Without it the enum's name fails every
// validator's primitive type-guard and the constraint is silently dropped.
func enumFieldPrimitive(f *ast.Field, ctx emitCtx) string {
	if f == nil || f.Type == nil || f.Type.Array || f.Type.Map != nil || f.Type.Named == nil || f.Type.Named.Name == nil {
		return ""
	}
	name := f.Type.Named.Name.String()
	var ed *ast.EnumDecl
	if ctx.pkg != nil {
		ed = ctx.pkg.Enums[name]
	}
	if ed == nil && ctx.resolver != nil {
		ed = ctx.resolver.LookupEnum(name)
	}
	if ed == nil {
		return ""
	}
	if firstEnumKind(ed) == ast.EnumInt {
		return "int"
	}
	return "string"
}

// scalarFieldLevelChecks emits the field-level decorators (`@lte`,
// `@maxLength`, `@pattern`, ...) declared ON a scalar-typed FIELD — the
// extra constraints a field stacks on top of the scalar's own ones
// (`unitCents Cents @lte(1000000)` narrows below Cents's base bound).
//
// Scalars are DEFINED Go types (`type Cents int`), so the field value
// (`v.UnitCents`, type Cents) fails the numeric/string type-guards and
// can't be handed to regexp/mail/time without a conversion. We deref +
// cast the value into a primitive local `_sv` ONCE, then run every
// field-level validator against that genuine primitive inside a single
// block (optional fields nil-guard first), so the field-level decorator
// matches a validator and fires.
func scalarFieldLevelChecks(f *ast.Field, access, primDSL string, ctx emitCtx) string {
	// Synthetic field typed as the bare primitive and NOT a pointer: the
	// deref happens in our wrapper, so `_sv` is already the value. The
	// field-level loop below drives off f.Decorators directly; sf carries
	// none of its own, because a copied `@nullable` (or any pointer-inducing
	// decorator) would make goFieldIsPointer(sf) true and re-add a nil-guard
	// + deref against the already-dereferenced value local.
	sf := &ast.Field{
		Name: f.Name,
		Type: &ast.TypeRef{
			Named: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{primDSL}}},
		},
	}
	const local = "_sv"
	var checks []string
	for _, d := range f.Decorators {
		v := validatorByName(d.Name)
		if v == nil {
			continue
		}
		if s := v.emit(sf, local, d, ctx); s != "" {
			checks = append(checks, s)
		}
	}
	if len(checks) == 0 {
		return ""
	}
	body := strings.Join(checks, "\n")
	primGo := scalarPrimitiveGo(primDSL)
	switch {
	case goFieldIsPointer(f, ctx.pkg, ctx.resolver):
		// Optional / @nullable scalar over a value primitive lowers to *T:
		// only run when present, and cast the dereferenced value.
		return fmt.Sprintf("if %s != nil {\n%s := %s(*%s)\n%s\n}", access, local, primGo, access, body)
	case scalarRefNilable(f.Type, ctx.pkg, ctx.resolver) && (f.Type.Optional || hasNullableDecorator(f.Decorators)):
		// Optional / @nullable scalar over a nilable primitive (bytes)
		// carries no pointer, but a nil value is the valid absent / null
		// state — guard, then cast the value directly (no deref).
		return fmt.Sprintf("if %s != nil {\n%s := %s(%s)\n%s\n}", access, local, primGo, access, body)
	default:
		return fmt.Sprintf("{\n%s := %s(%s)\n%s\n}", local, primGo, access, body)
	}
}

// scalarDeclHasValidators reports whether any of the scalar's
// decorators maps to a known validator emitter. Decorators like
// `@doc` / `@example` / `@default` are not validators and don't count.
// This is the single source of truth shared by the method-emission
// loop in [buildValidateData] and the dispatch decision in
// [typeRefNamedHasValidator] — keeping them in lock-step guarantees we
// never emit a `v.Field.Validate()` call against a scalar whose
// Validate() method was skipped (which would not compile).
func scalarDeclHasValidators(sd *ast.ScalarDecl) bool {
	if sd == nil {
		return false
	}
	for _, d := range sd.Decorators {
		if validatorByName(d.Name) != nil {
			return true
		}
	}
	return false
}

// scalarValidateChecks renders the body of a scalar's Validate()
// method. The receiver is a value of the defined scalar type (`func (v
// Email) Validate()`), so each per-decorator emitter runs against the
// receiver cast back to its underlying primitive (`string(v)`,
// `int(v)`, `[]byte(v)`). A synthetic field typed as that primitive
// drives the validator dispatch, targeting `v`.
func scalarValidateChecks(sd *ast.ScalarDecl, ctx emitCtx) []string {
	synth := &ast.Field{
		Name: sd.Name,
		Type: &ast.TypeRef{
			Named: &ast.NamedTypeRef{
				Name: &ast.QualifiedIdent{Parts: []string{scalarPrimitiveDSL(sd.Primitive)}},
			},
		},
	}
	access := scalarPrimitiveGo(sd.Primitive) + "(v)"
	var out []string
	for _, d := range sd.Decorators {
		v := validatorByName(d.Name)
		if v == nil {
			continue
		}
		if s := v.emit(synth, access, d, ctx); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// enumValidateChecks renders the body of an enum's Validate() method: a
// single switch that accepts every declared value and rejects anything
// else. Returns nil for a value-less enum (degenerate) so the emitted
// method is an empty `return nil` stub rather than an uncompilable
// `case :` switch. The receiver is the defined enum value (`func (v
// Color) Validate()`), so the switch compares `v` against the local
// constants directly.
func enumValidateChecks(ed *ast.EnumDecl) []string {
	if ed == nil || len(ed.EnumValues()) == 0 {
		return nil
	}
	return []string{enumSwitchBody(ed, "", "v", ed.Name)}
}
