package semantic

import (
	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/idents"
	"github.com/dropship-dev/craftgo/internal/lexer"
)

// builtinTypes aliases the canonical [idents.BuiltinTypes] table so
// existing local references keep compiling. Lives in [internal/idents]
// so the parser's disambiguation rules consult the same set without
// duplicating the entries - adding a primitive is now a one-place
// edit.
var builtinTypes = idents.BuiltinTypes

// checkImports validates per-file import sections for redundancy and
// alias collisions. The two diagnostics are complementary:
//
//   - [CodeImportDuplicate]    - same path imported twice in one file.
//   - [CodeImportAliasConflict] - two imports share the same alias
//     (explicit or implicit), making qualified references ambiguous.
//
// Both are file-scoped: a project that imports `shared` from two
// different files is fine; the same file doing it twice is not.
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

// importImplicitAlias returns the trailing path segment of an import
// path - the alias the DSL exposes when the user did not write one
// explicitly. Mirrors the resolution in [findDeclAcross] (LSP) and
// [importAliasSet] above.
func importImplicitAlias(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}

// checkLocalTypeRefs walks every NamedTypeRef in fields, mixins,
// scalar primitives, generic args, method request/response types, and
// middleware param types - for each single-segment name (no `pkg.`
// prefix) it verifies the name resolves to either a built-in
// primitive or a top-level declaration in the current package. The
// qualified-ref pass in imports.go covers the multi-segment case.
//
// Generic type parameters (`<T>` declared on a TypeDecl) are
// recognised inside that decl's body so `Page<T> { items T[] }` does
// not flag `T` as unknown. Single-segment names that match an
// import alias (or the implicit alias derived from the import path)
// are also skipped - the parser's recovery for malformed qualified
// refs (`shared.` with no symbol) leaves a single-part `shared` in
// the AST, and reporting that as an "unknown type" would mislead the
// user away from the real "expected identifier" defect.
func (a *analyzer) checkLocalTypeRefs(files []*ast.File) {
	for _, f := range files {
		imports := importAliasSet(f.Imports)
		for _, d := range f.Decls {
			switch v := d.(type) {
			case *ast.TypeDecl:
				typeParams := paramSet(v.TypeParams)
				for _, m := range v.Body {
					a.checkRefsInMember(m, typeParams, imports)
				}
			case *ast.ErrorDecl:
				for _, m := range v.Body {
					a.checkRefsInMember(m, nil, imports)
				}
			case *ast.ScalarDecl:
				// Scalar primitives are intentionally NOT validated
				// here - see [TestScalarUnknownPrimitiveSkipped]. The
				// type-compat pass tolerates unknown spellings on
				// purpose so future primitive additions don't break
				// projects that pulled them in via dependencies.
				_ = v
			case *ast.MiddlewareDecl:
				for _, p := range v.Params {
					a.checkLocalTypeRef(p.Type, nil, imports)
				}
			case *ast.ServiceDecl:
				for _, m := range v.Methods {
					if m.Request != nil {
						a.checkLocalNamedRef(m.Request, nil, imports)
					}
					if m.Response != nil && m.Response.Type != nil {
						a.checkLocalNamedRef(m.Response.Type, nil, imports)
					}
				}
			}
		}
	}
}

// importAliasSet collects every name that can legally appear as a
// qualifier in `pkg.Type`. Each import contributes either its explicit
// alias (`import x "..."` → `x`) or the trailing segment of its path
// (`import "from/x/y/z"` → `z`), matching how [findDeclAcross]
// resolves them in the LSP. Returns nil for empty input so callers
// can pass the result through cheaply.
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

// paramSet builds a quick-lookup set of type parameter names so the
// recursive type-ref walker can treat them as defined inside the
// generic body.
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

// checkRefsInMember dispatches on the type-body member shape - a
// [Field] carries a TypeRef; a [Mixin] is a NamedTypeRef on its own.
func (a *analyzer) checkRefsInMember(m ast.TypeMember, typeParams, imports map[string]bool) {
	switch v := m.(type) {
	case *ast.Field:
		a.checkLocalTypeRef(v.Type, typeParams, imports)
	case *ast.Mixin:
		a.checkLocalNamedRef(v.Ref, typeParams, imports)
	}
}

// checkLocalTypeRef descends into compound shapes (`map<K, V>`, generic
// arguments, array/optional suffixes) so every leaf ident is validated
// exactly once.
func (a *analyzer) checkLocalTypeRef(t *ast.TypeRef, typeParams, imports map[string]bool) {
	if t == nil {
		return
	}
	if t.Map != nil {
		a.checkLocalTypeRef(t.Map.Key, typeParams, imports)
		a.checkLocalTypeRef(t.Map.Value, typeParams, imports)
		return
	}
	if t.Named != nil {
		a.checkLocalNamedRef(t.Named, typeParams, imports)
	}
}

// checkLocalNamedRef handles the leaf case: when the qualified name has
// only one segment, it must be a builtin OR a declared TYPE-position
// symbol OR (for generic instantiation contexts) a type parameter in
// scope OR an import alias (the parser's recovery from a half-typed
// `pkg.` shape leaves a bare single-part `pkg` in the AST).
//
// Error declarations are deliberately NOT accepted as a type-position
// match even though they live in the same package. Errors are only
// valid inside `@errors(...)` decorator args (handled separately by
// [checkErrorRefs]); allowing them here would let a user write
// `field someUser UserNotFound` which compiles but produces a
// generated struct embedding an HTTP error type - a confusing
// category mistake. The diagnostic that fires when an error name
// is used as a field type carries an explicit hint pointing the
// user at `@errors(<name>)`.
func (a *analyzer) checkLocalNamedRef(n *ast.NamedTypeRef, typeParams, imports map[string]bool) {
	if n == nil || n.Name == nil {
		return
	}
	for _, arg := range n.Args {
		a.checkLocalTypeRef(arg, typeParams, imports)
	}
	if len(n.Name.Parts) != 1 {
		return
	}
	name := n.Name.Parts[0]
	if builtinTypes[name] {
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
		// Bare alias used in a type position - almost always a typo
		// for `alias.SomeType`, sometimes the parser's recovery from
		// a malformed `alias.` literal (in which case a parse error
		// already fires at the trailing dot). Either way the right
		// diagnostic points at the bare name and tells the user they
		// need a member after it.
		a.diag(n.Pos, n.Pos, lexer.SeverityError, CodeRefUnknownSymbol,
			"%q is an imported package, not a type - qualify it as %q.<TypeName>",
			name, name)
		return
	}
	a.diag(n.Pos, n.Pos, lexer.SeverityError, CodeRefUnknownSymbol,
		"unknown type %q (no built-in primitive, no declaration in package %q)",
		name, a.pkg.Name)
}

// isLocalType reports whether name resolves to a TYPE-position symbol
// in the current package - types, enums, or scalars. Errors are
// EXCLUDED on purpose: they belong only in `@errors(...)` and are
// surfaced with a dedicated diagnostic in [checkLocalNamedRef] rather
// than collapsed into the generic "unknown type" message.
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
