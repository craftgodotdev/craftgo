package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/prims"
)

// checkImports reports an import path, or an alias (explicit or implicit),
// repeated within one file.
func (a *analyzer) checkImports(files []*ast.File) {
	for _, f := range files {
		seenPath := map[string]*ast.Import{}
		seenAlias := map[string]*ast.Import{}
		for _, imp := range f.Imports {
			if prev, dup := seenPath[imp.Path]; dup {
				d := a.diag(imp.Pos, imp.Pos, lexer.SeverityError, CodeImportDuplicate,
					"duplicate import %q in this file", imp.Path)
				d.Related = related(prev.Pos, "first imported here")
				continue
			}
			seenPath[imp.Path] = imp
			alias := imp.Alias
			if alias == "" {
				alias = importImplicitAlias(imp.Path)
			}
			if prev, dup := seenAlias[alias]; dup {
				d := a.diag(imp.Pos, imp.Pos, lexer.SeverityError, CodeImportAliasConflict,
					"import alias %q already bound to %q - qualify one of them with an explicit alias",
					alias, prev.Path)
				d.Related = related(prev.Pos, "first bound here")
				continue
			}
			seenAlias[alias] = imp
		}
	}
}

// importImplicitAlias returns the alias of an import written without one:
// its last path segment.
func importImplicitAlias(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}

// checkLocalTypeRefs checks every bare type name in type and error bodies,
// event payloads and method requests and responses.
func (a *analyzer) checkLocalTypeRefs(files []*ast.File) {
	for _, f := range files {
		imports := importAliasSet(f.Imports)
		check := func(typeParams map[string]bool) func(*ast.NamedTypeRef) {
			return func(n *ast.NamedTypeRef) { a.checkLocalNamedRef(n, typeParams, imports) }
		}
		for _, d := range f.Decls {
			switch v := d.(type) {
			case *ast.TypeDecl:
				walkMemberRefs(v.Body, check(paramSet(v.TypeParams)))
			case *ast.ErrorDecl:
				walkMemberRefs(v.Body, check(nil))
			case *ast.ScalarDecl:
				// checkScalarTypeCompat checks a scalar's primitive.
			case *ast.EventDecl:
				if v.Payload != nil {
					v.Payload.Type.WalkNamedRefs(check(nil))
				}
			case *ast.ServiceDecl:
				for _, m := range v.Methods() {
					m.Request.WalkNamedRefs(check(nil))
					if m.Response != nil {
						m.Response.Type.WalkNamedRefs(check(nil))
					}
				}
			}
		}
	}
}

// walkMemberRefs calls fn on every named type the field types and mixins of
// members reach, in [ast.TypeRef.WalkNamedRefs] order.
func walkMemberRefs(members []ast.TypeMember, fn func(*ast.NamedTypeRef)) {
	for _, m := range members {
		switch v := m.(type) {
		case *ast.Field:
			v.Type.WalkNamedRefs(fn)
		case *ast.Mixin:
			v.Ref.WalkNamedRefs(fn)
		}
	}
}

// importAliasSet returns each import's alias, else its last path segment;
// nil for no imports.
func importAliasSet(imps []*ast.Import) map[string]bool {
	if len(imps) == 0 {
		return nil
	}
	out := map[string]bool{}
	for _, i := range imps {
		if i.Alias != "" {
			out[i.Alias] = true
			continue
		}
		base := i.Path
		for j := len(base) - 1; j >= 0; j-- {
			if base[j] == '/' {
				base = base[j+1:]
				break
			}
		}
		if base != "" {
			out[base] = true
		}
	}
	return out
}

// paramSet returns params as a set; nil when there are none.
func paramSet(params []string) map[string]bool {
	if len(params) == 0 {
		return nil
	}
	out := make(map[string]bool, len(params))
	for _, p := range params {
		out[p] = true
	}
	return out
}

// checkLocalNamedRef checks that a bare n names a built-in, a type parameter
// in scope, or a type, enum or scalar of this package; an error or an import
// alias gets its own diagnostic.
func (a *analyzer) checkLocalNamedRef(n *ast.NamedTypeRef, typeParams, imports map[string]bool) {
	if n.Name == nil || len(n.Name.Parts) != 1 {
		return
	}
	name := n.Name.Parts[0]
	if name == "object" {
		// `object` is a built-in spelling with no Go or OpenAPI form.
		a.diag(n.Pos, n.Pos, lexer.SeverityError, CodeRefUnknownSymbol,
			"`object` is not a usable field type - use `any` for an arbitrary JSON value, or `map<string, V>` / a declared `type` for a structured object")
		return
	}
	if prims.Is(name) {
		return
	}
	if typeParams != nil && typeParams[name] {
		return
	}
	if a.isLocalType(name) {
		return
	}
	if _, ok := a.pkg.Errors[name]; ok {
		a.diag(n.Pos, n.Pos, lexer.SeverityError, CodeRefUnknownSymbol,
			"%q is an error declaration, not a type - errors are only valid inside `@errors(...)`; declare a separate `type` if you need this shape as a field value",
			name)
		return
	}
	if imports != nil && imports[name] {
		// The parser also leaves a bare alias for a half-typed `alias.`.
		a.diag(n.Pos, n.Pos, lexer.SeverityError, CodeRefUnknownSymbol,
			"%q is an imported package, not a type - qualify it as %q.<TypeName>",
			name, name)
		return
	}
	a.diag(n.Pos, n.Pos, lexer.SeverityError, CodeRefUnknownSymbol,
		"unknown type %q (no built-in primitive, no declaration in package %q)",
		name, a.pkg.Name)
}

// isLocalType reports whether this package declares name as a type, enum
// or scalar.
func (a *analyzer) isLocalType(name string) bool {
	if _, ok := a.pkg.Types[name]; ok {
		return true
	}
	if _, ok := a.pkg.Enums[name]; ok {
		return true
	}
	if _, ok := a.pkg.Scalars[name]; ok {
		return true
	}
	return false
}
