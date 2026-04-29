package semantic

// Cross-package import + qualified-ref resolution. Runs after every
// file has been grouped into a package (by `package X` declaration)
// and each package has been individually analysed. For every file we:
//
//  1. Walk `import` declarations, validate the path against the
//     design filesystem, and store the per-file alias map for the LSP.
//  2. Walk every NamedTypeRef (in fields, mixin refs, method
//     request/response, generic args, map keys/values) and resolve
//     multi-part qualified names against the project's package set
//     keyed by the `package X` declaration name.
//
// Aliases (`import alias "path"`) are parsed and recorded but DO NOT
// drive resolution — qualified refs use the bare package name. The
// alias is preserved for IDE tooling that wants to surface "this
// import is referenced under name X".

import (
	"fmt"

	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/lexer"
)

// refResolver carries the per-call state for cross-package resolution.
// Kept private — external callers see only the [Project] result.
type refResolver struct {
	proj  *Project
	diags []Diagnostic
}

// processFile validates one file's imports + every qualified ref it
// contains.
func (r *refResolver) processFile(f *ast.File, designRoot string) {
	if f == nil {
		return
	}
	aliases := r.resolveImports(f, designRoot)
	if filename := fileFilename(f); filename != "" {
		r.proj.FileImports[filename] = aliases
	}

	currentPkg := ""
	if f.Package != nil {
		currentPkg = f.Package.Name
	}
	for _, d := range f.Decls {
		r.walkDeclRefs(d, currentPkg)
	}
}

// resolveImports walks f.Imports, validating each path and building
// the file's alias → relative-import-path map. The alias map is
// returned for storage on the project's [Project.FileImports] index;
// resolution itself uses package names, not aliases.
func (r *refResolver) resolveImports(f *ast.File, designRoot string) map[string]string {
	aliases := map[string]string{}
	currentPkg := ""
	if f.Package != nil {
		currentPkg = f.Package.Name
	}
	for _, imp := range f.Imports {
		path := imp.Path
		if path == "" {
			continue
		}
		if isEscapingPath(path) {
			r.diag(imp.Pos, lexer.SeverityError, CodeImportEscape,
				"import %q must be relative to the design root (no leading `/`, `./`, or `..`)", path)
			continue
		}
		if !folderExists(designRoot, path) {
			r.diag(imp.Pos, lexer.SeverityError, CodeImportUnresolved,
				"import %q does not match any folder under the design root", path)
			continue
		}
		// Self-import: `package X` importing a folder whose only
		// `.craftgo` files also declare `package X` — the import is
		// pulling files from itself. Detected when the imported
		// folder's package name matches the current file.
		if currentPkg != "" && currentPkg == folderPkg(path) {
			r.diag(imp.Pos, lexer.SeverityWarning, CodeImportSelf,
				"import %q resolves back into the current package %q (the files are merged anyway)",
				path, currentPkg)
		}
		alias := imp.Alias
		if alias == "" {
			alias = lastSegment(path)
		}
		// First-binding-wins for duplicate aliases — IDE may want to
		// surface the conflict but resolution doesn't depend on it.
		if _, dup := aliases[alias]; !dup {
			aliases[alias] = path
		}
	}
	return aliases
}

// walkDeclRefs descends into a top-level declaration, applying the
// qualified-ref check to every named type reference it contains.
func (r *refResolver) walkDeclRefs(d ast.Decl, currentPkg string) {
	switch dd := d.(type) {
	case *ast.TypeDecl:
		r.walkBodyRefs(dd.Body, currentPkg)
	case *ast.ErrorDecl:
		r.walkBodyRefs(dd.Body, currentPkg)
	case *ast.ServiceDecl:
		for _, m := range dd.Methods {
			if m.Request != nil {
				r.walkNamedRef(m.Request, currentPkg)
			}
			if m.Response != nil && m.Response.Type != nil {
				r.walkNamedRef(m.Response.Type, currentPkg)
			}
		}
	}
}

// walkBodyRefs walks fields + mixin refs in a type/error body.
func (r *refResolver) walkBodyRefs(members []ast.TypeMember, currentPkg string) {
	for _, m := range members {
		switch v := m.(type) {
		case *ast.Field:
			r.walkTypeRef(v.Type, currentPkg)
		case *ast.Mixin:
			r.walkNamedRef(v.Ref, currentPkg)
		}
	}
}

// walkTypeRef descends into a TypeRef, recursing through map keys,
// values, and generic arguments.
func (r *refResolver) walkTypeRef(t *ast.TypeRef, currentPkg string) {
	if t == nil {
		return
	}
	if t.Map != nil {
		r.walkTypeRef(t.Map.Key, currentPkg)
		r.walkTypeRef(t.Map.Value, currentPkg)
		return
	}
	if t.Named != nil {
		r.walkNamedRef(t.Named, currentPkg)
	}
}

// walkNamedRef applies the qualified-name validation to one named
// reference and recurses through its generic arguments. Single-part
// names are out of scope here — the per-package analyser already
// resolves them. Multi-part names look up the prefix as a Package
// name in the project; failures emit [CodeRefUnknownPackage] or
// [CodeRefUnknownSymbol].
func (r *refResolver) walkNamedRef(n *ast.NamedTypeRef, currentPkg string) {
	if n == nil || n.Name == nil {
		return
	}
	for _, arg := range n.Args {
		r.walkTypeRef(arg, currentPkg)
	}
	parts := n.Name.Parts
	if len(parts) < 2 {
		return
	}
	if len(parts) > 2 {
		r.diag(n.Pos, lexer.SeverityError, CodeQualifiedRef,
			"qualified reference %q has too many segments (max 1 package prefix)", n.Name.String())
		return
	}
	pkgName, sym := parts[0], parts[1]
	// Self-qualified `currentPkg.Type` is allowed but redundant —
	// resolve it and don't fire any diagnostic.
	target := r.proj.Packages[pkgName]
	if target == nil {
		r.diag(n.Pos, lexer.SeverityError, CodeRefUnknownPackage,
			"package %q is not declared anywhere in the project", pkgName)
		return
	}
	if !packageHasSymbol(target, sym) {
		r.diag(n.Pos, lexer.SeverityError, CodeRefUnknownSymbol,
			"package %q has no symbol %q", pkgName, sym)
	}
}

// packageHasSymbol reports whether sym is declared in pkg's symbol
// tables. We accept any kind (type, enum, error, scalar) — DSL
// resolution doesn't distinguish at the reference site.
func packageHasSymbol(pkg *Package, sym string) bool {
	if pkg == nil {
		return false
	}
	if _, ok := pkg.Types[sym]; ok {
		return true
	}
	if _, ok := pkg.Enums[sym]; ok {
		return true
	}
	if _, ok := pkg.Errors[sym]; ok {
		return true
	}
	if _, ok := pkg.Scalars[sym]; ok {
		return true
	}
	return false
}

// folderPkg returns the conventional `package X` name a folder is
// expected to declare — by convention, the last path segment. Used
// for self-import detection without re-parsing the folder's files.
// A folder whose actual `package X` declaration diverges from this
// convention will not trip the warning, which is fine: the
// declaration is the source of truth and the import is informational.
func folderPkg(importPath string) string {
	return lastSegment(importPath)
}

// isEscapingPath reports whether the import path uses syntax that
// would escape the design root or signal an unsupported absolute
// reference.
func isEscapingPath(p string) bool {
	if len(p) == 0 {
		return false
	}
	if p[0] == '/' {
		return true
	}
	if len(p) >= 2 && p[0] == '.' && p[1] == '/' {
		return true
	}
	if len(p) >= 3 && p[0] == '.' && p[1] == '.' && p[2] == '/' {
		return true
	}
	return p == ".." || p == "."
}

// diag is a thin wrapper that appends a diagnostic with End = Pos.
// Cross-package diagnostics don't have a clean trailing position the
// way decorator names do; the LSP renders an empty range as a single
// column underline.
func (r *refResolver) diag(pos lexer.Position, sev lexer.Severity, code, format string, args ...any) {
	r.diags = append(r.diags, Diagnostic{
		Pos:      pos,
		End:      pos,
		Severity: sev,
		Code:     code,
		Msg:      fmt.Sprintf(format, args...),
	})
}

// lastSegment returns the trailing path segment, used as the default
// alias for `import "auth/types"` (alias = "types").
func lastSegment(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}
