package semantic

import (
	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/lexer"
)

// builtinTypes is the closed set of primitive type spellings the DSL
// recognises out of the box. Any single-segment [ast.NamedTypeRef]
// whose name is not in this set MUST resolve to a declaration in the
// current package; otherwise the analyser fires
// [CodeRefUnknownSymbol] so typos like `strg` instead of `string`
// surface as errors instead of silently passing through to codegen.
var builtinTypes = map[string]bool{
	"string": true, "bool": true,
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"float32": true, "float64": true,
	"bytes":  true,
	"any":    true,
	"object": true, // permissive bag-of-fields, used in `@example({...})`
	"file":   true,
}

// checkImports validates per-file import sections for redundancy and
// alias collisions. The two diagnostics are complementary:
//
//   - [CodeImportDuplicate]    — same path imported twice in one file.
//   - [CodeImportAliasConflict] — two imports share the same alias
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
					"import alias %q already bound to %q — qualify one of them with an explicit alias",
					alias, prev.Path)
				d.Related = related(prev.Pos, "first bound here")
				continue
			}
			seenAlias[alias] = imp
		}
	}
}

// importImplicitAlias returns the trailing path segment of an import
// path — the alias the DSL exposes when the user did not write one
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
// middleware param types — for each single-segment name (no `pkg.`
// prefix) it verifies the name resolves to either a built-in
// primitive or a top-level declaration in the current package. The
// qualified-ref pass in imports.go covers the multi-segment case.
//
// Generic type parameters (`<T>` declared on a TypeDecl) are
// recognised inside that decl's body so `Page<T> { items T[] }` does
// not flag `T` as unknown. Single-segment names that match an
// import alias (or the implicit alias derived from the import path)
// are also skipped — the parser's recovery for malformed qualified
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
				// here — see [TestScalarUnknownPrimitiveSkipped]. The
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

// checkRefsInMember dispatches on the type-body member shape — a
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
// only one segment, it must be a builtin OR a declared symbol OR (for
// generic instantiation contexts) a type parameter in scope OR an
// import alias (the parser's recovery from a half-typed `pkg.` shape
// leaves a bare single-part `pkg` in the AST).
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
	if a.isLocalSymbol(name) {
		return
	}
	if imports != nil && imports[name] {
		// Bare alias used in a type position — almost always a typo
		// for `alias.SomeType`, sometimes the parser's recovery from
		// a malformed `alias.` literal (in which case a parse error
		// already fires at the trailing dot). Either way the right
		// diagnostic points at the bare name and tells the user they
		// need a member after it.
		a.diag(n.Pos, n.Pos, lexer.SeverityError, CodeRefUnknownSymbol,
			"%q is an imported package, not a type — qualify it as %q.<TypeName>",
			name, name)
		return
	}
	a.diag(n.Pos, n.Pos, lexer.SeverityError, CodeRefUnknownSymbol,
		"unknown type %q (no built-in primitive, no declaration in package %q)",
		name, a.pkg.Name)
}

// isLocalSymbol reports whether name is declared in the current
// package's symbol tables. Mirrors [packageHasSymbol] but operates on
// the analyser's in-progress [Package] rather than a finalised one.
// [a.pkg] is initialised in [AnalyzeWith] before this method runs, so
// no nil guard is needed.
func (a *analyzer) isLocalSymbol(name string) bool {
	if _, ok := a.pkg.Types[name]; ok {
		return true
	}
	if _, ok := a.pkg.Enums[name]; ok {
		return true
	}
	if _, ok := a.pkg.Errors[name]; ok {
		return true
	}
	if _, ok := a.pkg.Scalars[name]; ok {
		return true
	}
	return false
}
