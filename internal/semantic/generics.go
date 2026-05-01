package semantic

// Generic instantiation validation. Per README §"Type composition", a
// generic decl is `Foo<T>` and an instance is `Foo<User>`; the codegen
// renames concrete instances to `FooOfUser` / `FooOfUserAndOrg`. This
// pass enforces that every reference to a generic decl supplies
// exactly the right number of type arguments, and that no non-generic
// type is given args.
//
// Diagnostic codes:
//
//   - [CodeGenericArity]      - wrong arg count for a generic decl.
//   - [CodeGenericNonGeneric] - args supplied to a non-generic type.
//
// References to type-parameter idents inside a generic decl's body
// (`f T[]` inside `type Page<T> { ... }`) are NOT flagged: T is in
// scope as a type variable. The walker tracks the enclosing decl's
// TypeParams to make that distinction.

import (
	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/lexer"
)

// checkGenerics walks every declared type / error body and every
// service method's request/response, validating each [NamedTypeRef]'s
// arg count against the referenced decl. Map keys/values and nested
// generic args are visited recursively so `map<string, Page<User>>`
// gets the same scrutiny as a top-level field type.
func (a *analyzer) checkGenerics() {
	for _, td := range a.pkg.Types {
		a.walkBodyGenerics(td.Body, td.TypeParams)
	}
	for _, ed := range a.pkg.Errors {
		// Errors carry no type params today, so the param-set is empty.
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

// walkBodyGenerics walks every Field type and every Mixin ref in
// members, propagating the enclosing decl's type params so a `T[]`
// inside `Page<T>` doesn't trip the "unknown type" branch.
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

// walkTypeRefGenerics descends into a TypeRef. Map types recurse into
// both key and value; named refs delegate to walkNamedRefGenerics.
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

// walkNamedRefGenerics validates one named reference and its generic
// arguments. The enclosing decl's TypeParams are passed so single-part
// names matching one of them are recognised as type variables and
// skipped.
func (a *analyzer) walkNamedRefGenerics(n *ast.NamedTypeRef, typeParams []string) {
	if n == nil || n.Name == nil {
		return
	}
	// Recurse into args first - even if the outer ref is bogus, we
	// still want to flag a malformed nested arg.
	for _, arg := range n.Args {
		a.walkTypeRefGenerics(arg, typeParams)
	}
	// Qualified refs are handled by [analyzer.checkQualifiedRefs];
	// we don't double-report here.
	if len(n.Name.Parts) != 1 {
		return
	}
	name := n.Name.Parts[0]
	// Type-parameter ref inside a generic decl body. T is an in-scope
	// type variable, not a type lookup.
	if inSet(name, typeParams) {
		// A type variable used with arguments is meaningless ("T<X>"
		// makes no sense in Go's parametric model). Flag it.
		if len(n.Args) > 0 {
			a.diag(n.Pos, n.Pos, lexer.SeverityError, CodeGenericNonGeneric,
				"type parameter %q does not take generic arguments", name)
		}
		return
	}
	td, ok := a.pkg.Types[name]
	if !ok {
		// Unknown type - placement / qualified-ref / built-in handling
		// covers the diagnostic elsewhere. Bail out to avoid a
		// confusing "expects 0 args" message.
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
