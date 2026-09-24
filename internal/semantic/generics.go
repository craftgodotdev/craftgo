package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// checkGenerics checks the generic arguments of every named reference in
// type and error bodies and in method requests and responses, map keys,
// values and nested arguments included.
func (a *analyzer) checkGenerics() {
	for _, td := range a.pkg.Types {
		a.walkBodyGenerics(td.Body, td.TypeParams)
	}
	for _, ed := range a.pkg.Errors {
		a.walkBodyGenerics(ed.Body, nil)
	}
	for _, si := range a.pkg.Services {
		for _, m := range si.Methods {
			a.walkNamedRefGenerics(m.Request, nil)
			if m.Response != nil {
				a.walkNamedRefGenerics(m.Response.Type, nil)
			}
		}
	}
}

// walkBodyGenerics walks every field type and mixin in members with the
// enclosing decl's type parameters in scope.
func (a *analyzer) walkBodyGenerics(members []ast.TypeMember, typeParams []string) {
	for _, m := range members {
		switch v := m.(type) {
		case *ast.Field:
			a.walkTypeRefGenerics(v.Type, typeParams)
		case *ast.Mixin:
			a.walkNamedRefGenerics(v.Ref, typeParams)
		}
	}
}

// walkTypeRefGenerics walks t, a map's key and value included.
func (a *analyzer) walkTypeRefGenerics(t *ast.TypeRef, typeParams []string) {
	if t == nil {
		return
	}
	if t.Map != nil {
		a.walkTypeRefGenerics(t.Map.Key, typeParams)
		a.walkTypeRefGenerics(t.Map.Value, typeParams)
		return
	}
	if t.Named != nil {
		a.walkNamedRefGenerics(t.Named, typeParams)
	}
}

// walkNamedRefGenerics checks n's generic arguments and its arity; a bare
// name in typeParams is a type variable, not a type.
func (a *analyzer) walkNamedRefGenerics(n *ast.NamedTypeRef, typeParams []string) {
	if n == nil || n.Name == nil {
		return
	}
	for _, arg := range n.Args {
		if arg != nil && arg.Optional {
			a.diag(arg.Pos, arg.Pos, lexer.SeverityError, CodeGenericOptionalArg,
				"a generic type argument cannot be optional (`?`) - the optionality has no well-defined position after substitution, so the Go type and the OpenAPI schema would disagree. Declare the nullability on a field inside the generic (e.g. `type Box<T> { item T? }`) instead.")
		}
		a.walkTypeRefGenerics(arg, typeParams)
	}
	// The project pass checks the arity of qualified refs.
	if len(n.Name.Parts) != 1 {
		return
	}
	name := n.Name.Parts[0]
	if inSet(name, typeParams) {
		if len(n.Args) > 0 {
			a.diag(n.Pos, n.Pos, lexer.SeverityError, CodeGenericNonGeneric,
				"type parameter %q does not take generic arguments", name)
		}
		return
	}
	td, ok := a.pkg.Types[name]
	if !ok {
		return
	}
	want := len(td.TypeParams)
	got := len(n.Args)
	switch {
	case want == 0 && got > 0:
		a.diag(n.Pos, n.Pos, lexer.SeverityError, CodeGenericNonGeneric,
			"%s is not a generic type but received %d argument(s)", name, got)
	case want > 0 && got != want:
		a.diag(n.Pos, n.Pos, lexer.SeverityError, CodeGenericArity,
			"%s expects %d generic argument(s), got %d", name, want, got)
	}
}
