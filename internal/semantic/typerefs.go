package semantic

import (
	"fmt"
	"slices"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/prims"
)

// checkTypeRefs checks every named type reference of the files'
// declarations.
func (a *analyzer) checkTypeRefs(files []*ast.File) {
	for _, f := range files {
		imports := importAliasSet(f.Imports)
		for _, d := range f.Decls {
			walkTypeRefs(d, func(n *ast.NamedTypeRef, typeParams []string, mixin bool) {
				a.checkTypeRef(n, typeParams, imports, mixin)
			})
		}
	}
}

// walkTypeRefs calls visit on every named type reference d holds: the field
// types and mixins of a type or error body, an event payload, and each
// method's request and response, through map keys and values and generic
// arguments. typeParams are the type parameters in scope; mixin marks the
// name a mixin embeds.
func walkTypeRefs(d ast.Decl, visit func(n *ast.NamedTypeRef, typeParams []string, mixin bool)) {
	walkBody := func(body []ast.TypeMember, typeParams []string) {
		for _, m := range body {
			switch v := m.(type) {
			case *ast.Field:
				v.Type.WalkNamedRefs(func(n *ast.NamedTypeRef) { visit(n, typeParams, false) })
			case *ast.Mixin:
				v.Ref.WalkNamedRefs(func(n *ast.NamedTypeRef) { visit(n, typeParams, n == v.Ref) })
			}
		}
	}
	outside := func(n *ast.NamedTypeRef) { visit(n, nil, false) }
	switch v := d.(type) {
	case *ast.TypeDecl:
		walkBody(v.Body, v.TypeParams)
	case *ast.ErrorDecl:
		walkBody(v.Body, nil)
	case *ast.EventDecl:
		if v.Payload != nil {
			v.Payload.Type.WalkNamedRefs(outside)
		}
	case *ast.ServiceDecl:
		for _, m := range v.Methods() {
			m.Request.WalkNamedRefs(outside)
			if m.Response != nil {
				m.Response.Type.WalkNamedRefs(outside)
			}
		}
	}
}

// checkTypeRef checks one named reference: its generic arguments are not
// optional, it names a built-in, a type parameter in scope or a type, enum
// or scalar, and its arguments fit what it names. What a mixin embeds and
// its arity are checked by [analyzer.processMixin].
func (a *analyzer) checkTypeRef(n *ast.NamedTypeRef, typeParams []string, imports map[string]bool, mixin bool) {
	if n.Name == nil || len(n.Name.Parts) == 0 {
		return
	}
	for _, arg := range n.Args {
		if arg != nil && arg.Optional {
			a.diag(arg.Pos, arg.Pos, lexer.SeverityError, CodeGenericOptionalArg,
				"a generic type argument cannot be optional (`?`) - the optionality has no well-defined position after substitution, so the Go type and the OpenAPI schema would disagree. Declare the nullability on a field inside the generic (e.g. `type Box<T> { item T? }`) instead.")
		}
	}
	parts := n.Name.Parts
	switch {
	case len(parts) > 2:
		a.diag(n.Pos, n.Pos, lexer.SeverityError, CodeQualifiedRef,
			"qualified reference %q has too many segments (max 1 package prefix)", n.Name.String())
		return
	case len(parts) == 2 && parts[0] == a.pkg.Name:
		a.diag(n.Pos, n.Pos, lexer.SeverityError, CodeQualifiedRef,
			"redundant self-qualification %q - a type in its own package is referenced by its bare name; write %q",
			n.Name.String(), parts[1])
		return
	case len(parts) == 1 && parts[0] == "object":
		a.diag(n.Pos, n.Pos, lexer.SeverityError, CodeRefUnknownSymbol,
			"`object` is not a usable field type - use `any` for an arbitrary JSON value, or `map<string, V>` / a declared `type` for a structured object")
		return
	case len(parts) == 1 && prims.Is(parts[0]):
		if !mixin {
			a.checkArity(n, nil)
		}
		return
	case len(parts) == 1 && slices.Contains(typeParams, parts[0]):
		if len(n.Args) > 0 {
			a.diag(n.Pos, n.Pos, lexer.SeverityError, CodeGenericNonGeneric,
				"type parameter %q does not take generic arguments", parts[0])
		}
		return
	}
	pkg, sym := a.proj.resolve(a.pkg.Name, n.Name)
	if pkg == nil {
		a.diag(n.Pos, n.Pos, lexer.SeverityError, CodeRefUnknownPackage,
			"package %q is not declared anywhere in the project", parts[0])
		return
	}
	kinds := TypeRefDecls
	if mixin {
		kinds = mixinNamedKinds
	}
	target := pkg.Decl(sym, kinds)
	switch {
	case target != nil:
		if !mixin {
			a.checkArity(n, target)
		}
	case pkg.Errors[sym] != nil:
		a.diag(n.Pos, n.Pos, lexer.SeverityError, CodeRefUnknownSymbol, "%s", errorAsTypeMsg(n.Name.String()))
	case len(parts) == 2:
		a.diag(n.Pos, n.Pos, lexer.SeverityError, CodeRefUnknownSymbol,
			"package %q has no symbol %q", parts[0], sym)
	case imports[sym]:
		// The parser also leaves a bare alias for a half-typed `alias.`.
		a.diag(n.Pos, n.Pos, lexer.SeverityError, CodeRefUnknownSymbol,
			"%q is an imported package, not a type - qualify it as %q.<TypeName>", sym, sym)
	default:
		a.diag(n.Pos, n.Pos, lexer.SeverityError, CodeRefUnknownSymbol,
			"unknown type %q (no built-in primitive, no declaration in package %q)", sym, a.pkg.Name)
	}
}

// errorAsTypeMsg words the diagnostic for an error declaration named where a
// type belongs.
func errorAsTypeMsg(name string) string {
	return fmt.Sprintf("%q is an error declaration, not a type - errors are only valid inside `@errors(...)`; declare a separate `type` if you need this shape as a field value", name)
}

// checkArity reports generic arguments that do not fit target: a generic
// type takes exactly its type parameters; any other type, an enum, a scalar
// or a built-in (a nil target) takes none.
func (a *analyzer) checkArity(n *ast.NamedTypeRef, target ast.Decl) {
	want := 0
	if td, ok := target.(*ast.TypeDecl); ok {
		want = len(td.TypeParams)
	}
	got := len(n.Args)
	switch {
	case want == 0 && got > 0:
		a.diag(n.Pos, n.Pos, lexer.SeverityError, CodeGenericNonGeneric,
			"%s is not a generic type but received %d argument(s)", n.Name.String(), got)
	case want > 0 && got != want:
		a.diag(n.Pos, n.Pos, lexer.SeverityError, CodeGenericArity,
			"%s expects %d generic argument(s), got %d", n.Name.String(), want, got)
	}
}

// isQualifiedTypeRef reports whether t names a qualified `pkg.Name` symbol.
func isQualifiedTypeRef(t *ast.TypeRef) bool {
	if t == nil || t.Named == nil || t.Named.Name == nil {
		return false
	}
	return len(t.Named.Name.Parts) >= 2
}
