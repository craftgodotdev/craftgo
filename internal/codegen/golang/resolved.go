package golang

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// resolvedField is a [semantic.ResolvedField] with its Go rendering.
type resolvedField struct {
	semantic.ResolvedField

	GoName string // Go selector of the field: its exported name, behind its embed path when shadowed

	IsPointer bool // Go type is a pointer: a wrapped optional or @nullable field, or a file
}

// decorate wraps one resolved field with its Go rendering.
func decorate(rf semantic.ResolvedField) resolvedField {
	out := resolvedField{ResolvedField: rf, GoName: rf.Name, IsPointer: rf.GoPointer()}
	if out.GoName == "" && rf.Field != nil {
		out.GoName = idents.GoFieldName(rf.Field.Name)
	}
	return out
}

// resolveRequestFields resolves m's request fields with method context
// (auto-binding) applied, then adds the Go rendering.
func resolveRequestFields(m *ast.Method, pkg *semantic.Package, r *projectResolver) []resolvedField {
	in := semantic.RequestFields(m, pkg, r.Resolver, resolvedGoFieldNames)
	out := make([]resolvedField, len(in))
	for i, rf := range in {
		out[i] = decorate(rf)
	}
	return out
}
