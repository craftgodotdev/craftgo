// Resolved field/type IR: the single, fully-resolved view of a type's
// fields that every codegen stage consumes - so no stage re-walks the AST
// and re-derives a field fact (is-on-wire, is-required, is-pointer, wire
// name, default value) differently from another and drifts.
//
// [resolveFields] computes each fact ONCE, so a consumer that reads a
// ResolvedField gets a value that cannot disagree with another consumer's.
package golang

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// resolvedField is the resolved view of one field after mixin flattening
// and generic-argument substitution: the layer-agnostic facts from the
// semantic IR (category, primitive, home package, nilability) plus the Go
// rendering derived from them. Every value is computed from the canonical
// helper, so the field is the single source of truth a stage reads
// instead of recomputing.
type resolvedField struct {
	semantic.ResolvedField

	GoName string // exported Go field identifier
	GoType string // final Go type, including any *T nullable wrap

	IsPointer bool // generated Go type is a pointer: a wrapped optional / @nullable field, or a file
}

// WireName returns the field's wire parameter name for its explicit
// binding (the decorator's name arg, or the field name). Empty for a body
// or sensitive field.
func (rf resolvedField) WireName() string {
	switch rf.Binding {
	case wire.BindPath, wire.BindQuery, wire.BindHeader, wire.BindCookie, wire.BindForm:
		return wire.WireName(rf.Field, rf.Binding.String())
	default:
		return ""
	}
}

// resolveField adds this package's rendering - the Go identifier, the Go
// type text and the pointer decision - to the layer-agnostic facts
// [semantic.ResolveField] computes.
func resolveField(f *ast.Field, pkg *semantic.Package, r *projectResolver) resolvedField {
	return decorate(semantic.ResolveField(f, pkg, r.Project()), pkg, r)
}

// decorate wraps one resolved field with its Go rendering.
func decorate(rf semantic.ResolvedField, pkg *semantic.Package, r *projectResolver) resolvedField {
	f := rf.Field
	out := resolvedField{ResolvedField: rf, GoName: rf.Name}
	if out.GoName == "" && f != nil {
		out.GoName = goFieldName(f.Name)
	}
	if f != nil {
		out.GoType = goFieldType(f, pkg, r)
		out.IsPointer = goFieldIsPointer(f, pkg, r)
	}
	return out
}

// decorateAll is [decorate] over a resolved field list.
func decorateAll(in []semantic.ResolvedField, pkg *semantic.Package, r *projectResolver) []resolvedField {
	out := make([]resolvedField, len(in))
	for i, rf := range in {
		out[i] = decorate(rf, pkg, r)
	}
	return out
}

// resolveFields resolves td's fields with this package's Go names.
func resolveFields(td *ast.TypeDecl, pkg *semantic.Package, r *projectResolver) []resolvedField {
	return resolveFieldsWithPrefix(td, "", pkg, r)
}

// resolveFieldsWithPrefix is [resolveFields] with a package-prefix context.
func resolveFieldsWithPrefix(td *ast.TypeDecl, prefix string, pkg *semantic.Package, r *projectResolver) []resolvedField {
	return decorateAll(semantic.ResolveFieldsWithPrefix(td, prefix, pkg, r.Resolver, resolvedGoFieldNames), pkg, r)
}

// resolveRequestFields resolves m's request fields with method context
// (auto-binding) applied, then adds the Go rendering.
func resolveRequestFields(m *ast.Method, pkg *semantic.Package, r *projectResolver) []resolvedField {
	return decorateAll(semantic.RequestFields(m, pkg, r.Resolver, resolvedGoFieldNames), pkg, r)
}

// fieldNeedsNilGuard reports whether a constraint check must nil-guard
// the field before len() / deref: every optional (`?`) or `@nullable`
// field, which lowers either to a pointer or to a nilable Go value whose
// nil is the valid "absent / null" state.
func fieldNeedsNilGuard(f *ast.Field) bool {
	return semantic.FieldIsOptional(f)
}
