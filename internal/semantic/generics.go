package semantic

import (
	"slices"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// checkGenerics checks the generic arguments of every named reference in
// type and error bodies and in method requests and responses, map keys,
// values and nested arguments included.
func (a *analyzer) checkGenerics() {
	check := func(typeParams []string) func(*ast.NamedTypeRef) {
		return func(n *ast.NamedTypeRef) { a.checkNamedRefGenerics(n, typeParams) }
	}
	for _, td := range a.pkg.Types {
		walkMemberRefs(td.Body, check(td.TypeParams))
	}
	for _, ed := range a.pkg.Errors {
		walkMemberRefs(ed.Body, check(nil))
	}
	for _, si := range a.pkg.Services {
		for _, m := range si.Methods {
			m.Request.WalkNamedRefs(check(nil))
			if m.Response != nil {
				m.Response.Type.WalkNamedRefs(check(nil))
			}
		}
	}
}

// checkNamedRefGenerics checks n's generic arguments and its arity; a bare
// name in typeParams is a type variable, not a type.
func (a *analyzer) checkNamedRefGenerics(n *ast.NamedTypeRef, typeParams []string) {
	if n.Name == nil {
		return
	}
	for _, arg := range n.Args {
		if arg != nil && arg.Optional {
			a.diag(arg.Pos, arg.Pos, lexer.SeverityError, CodeGenericOptionalArg,
				"a generic type argument cannot be optional (`?`) - the optionality has no well-defined position after substitution, so the Go type and the OpenAPI schema would disagree. Declare the nullability on a field inside the generic (e.g. `type Box<T> { item T? }`) instead.")
		}
	}
	// The project pass checks the arity of qualified refs.
	if len(n.Name.Parts) != 1 {
		return
	}
	name := n.Name.Parts[0]
	if slices.Contains(typeParams, name) {
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
