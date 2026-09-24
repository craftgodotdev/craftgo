package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// LevelNames returns the identifiers a target renders one struct level's
// fields with, in body order. Nil leaves [ResolvedField.Name] empty.
type LevelNames func([]ast.TypeMember) []string

// LookupMethodType returns the type ref names through r and the ref's
// package qualifier ("" when bare), the prefix its bare mixins resolve in.
// The type is nil when r does not resolve it.
func LookupMethodType(ref *ast.NamedTypeRef, r *Resolver) (*ast.TypeDecl, string) {
	if ref == nil || ref.Name == nil {
		return nil, ""
	}
	name := ref.Name.String()
	prefix := ""
	if parts := ref.Name.Parts; len(parts) == 2 {
		prefix = parts[0]
	}
	return r.LookupType(name), prefix
}

// ResolveFields resolves every field [FlattenFields] returns for td, prefix,
// r and levelNames; a bare type name resolves in pkg.
func ResolveFields(td *ast.TypeDecl, prefix string, pkg *Package, r *Resolver, levelNames LevelNames) []ResolvedField {
	flat := FlattenFields(td, prefix, r, levelNames)
	out := make([]ResolvedField, 0, len(flat))
	for _, ff := range flat {
		rf := ResolveField(ff.Field, pkg, r.Project())
		rf.Name = ff.Name
		out = append(out, rf)
	}
	return out
}

// RequestFields resolves m's request fields and auto-binds each one with no
// binding decorator and no @sensitive: to @path when its name is a route
// variable (@prefix included), else to @query on a body-less verb.
func RequestFields(m *ast.Method, pkg *Package, r *Resolver, levelNames LevelNames) []ResolvedField {
	if m == nil || m.Request == nil {
		return nil
	}
	td, prefix := LookupMethodType(m.Request, r)
	if td == nil {
		return nil
	}
	pathNames := methodRoutePathVars(m, pkg.Services)
	bodyVerb := wire.IsBodyVerb(m.Verb)
	fields := ResolveFields(td, prefix, pkg, r, levelNames)
	for i := range fields {
		rf := &fields[i]
		// An explicit @body also reads as BindBody.
		if _, explicit := wire.BindingKind(rf.Field.Decorators); explicit || rf.Binding != wire.BindBody {
			continue
		}
		rf.Binding, rf.AutoBound = wire.RequestFieldBinding(rf.Field, pathNames, bodyVerb)
		rf.OnWireBody = rf.Binding == wire.BindBody
	}
	return fields
}
