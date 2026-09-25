package golang

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// resolvedField is a [semantic.ResolvedField] with its Go rendering.
type resolvedField struct {
	semantic.ResolvedField

	GoName string // Go selector of the field: its exported name, behind its embed path when shadowed
	GoType string // final Go type, including any *T nullable wrap

	IsPointer bool // Go type is a pointer: a wrapped optional or @nullable field, or a file
}

// WireName returns the field's name in its path, query, header, cookie or form
// binding, or "" for a body or sensitive field.
func (rf resolvedField) WireName() string {
	if rf.Binding.IsParam() {
		return wire.WireName(rf.Field, rf.Binding)
	}
	return ""
}

// resolveField is [semantic.ResolveField] with f's Go rendering.
func resolveField(f *ast.Field, pkg *semantic.Package, r *projectResolver) resolvedField {
	return decorate(semantic.ResolveField(f, pkg, r.Project()), pkg, r)
}

// decorate wraps one resolved field with its Go rendering.
func decorate(rf semantic.ResolvedField, pkg *semantic.Package, r *projectResolver) resolvedField {
	f := rf.Field
	out := resolvedField{ResolvedField: rf, GoName: rf.Name}
	if out.GoName == "" && f != nil {
		out.GoName = idents.GoFieldName(f.Name)
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

// resolveRequestFields resolves m's request fields with method context
// (auto-binding) applied, then adds the Go rendering.
func resolveRequestFields(m *ast.Method, pkg *semantic.Package, r *projectResolver) []resolvedField {
	return decorateAll(semantic.RequestFields(m, pkg, r.Resolver, resolvedGoFieldNames), pkg, r)
}

// fieldNeedsNilGuard reports whether nil is a valid value of f: it is optional
// or @nullable.
func fieldNeedsNilGuard(f *ast.Field) bool {
	return semantic.FieldIsOptional(f)
}
