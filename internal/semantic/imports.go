package semantic

import (
	"fmt"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// refResolver runs the project-wide checks and collects their diagnostics.
type refResolver struct {
	proj     *Project
	diags    []Diagnostic
	basePath string // [Options.BasePath]
	// fileCase is the defaulted output.fileCase, which names an ungrouped
	// service's directory.
	fileCase string
}

// processFile checks one file's import paths and qualified type references.
func (r *refResolver) processFile(f *ast.File, designRoot string) {
	if f == nil {
		return
	}
	r.resolveImports(f, designRoot)
	currentPkg := ""
	if f.Package != nil {
		currentPkg = f.Package.Name
	}
	for _, d := range f.Decls {
		r.walkDeclRefs(d, currentPkg)
	}
}

// resolveImports checks each import path against the design root. A
// qualified reference names a package, never an import alias.
func (r *refResolver) resolveImports(f *ast.File, designRoot string) {
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
		if designRoot != "" && !folderExists(designRoot, path) {
			r.diag(imp.Pos, lexer.SeverityError, CodeImportUnresolved,
				"import %q does not match any folder under the design root", path)
			continue
		}
		// A folder named after the file's own package is a self-import.
		if currentPkg != "" && currentPkg == folderPkg(path) {
			r.diag(imp.Pos, lexer.SeverityWarning, CodeImportSelf,
				"import %q resolves back into the current package %q (the files are merged anyway)",
				path, currentPkg)
		}
	}
}

// walkDeclRefs checks every qualified type reference in d.
func (r *refResolver) walkDeclRefs(d ast.Decl, currentPkg string) {
	switch dd := d.(type) {
	case *ast.TypeDecl:
		r.walkBodyRefs(dd.Body, currentPkg)
	case *ast.ErrorDecl:
		r.walkBodyRefs(dd.Body, currentPkg)
	case *ast.EventDecl:
		if dd.Payload != nil && dd.Payload.Type != nil {
			r.walkNamedRef(dd.Payload.Type, currentPkg)
		}
	case *ast.ServiceDecl:
		for _, m := range dd.Methods() {
			if m.Request != nil {
				r.walkNamedRef(m.Request, currentPkg)
			}
			if m.Response != nil && m.Response.Type != nil {
				r.walkNamedRef(m.Response.Type, currentPkg)
			}
		}
	}
}

// walkBodyRefs walks the field types and mixins of a type or error body.
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

// walkTypeRef walks t, a map's key and value included.
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

// walkNamedRef walks n's generic arguments and, when n is qualified,
// checks its package, symbol and generic arity. Bare names are checked
// per package.
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
	if pkgName == currentPkg && currentPkg != "" {
		r.diag(n.Pos, lexer.SeverityError, CodeQualifiedRef,
			"redundant self-qualification %q - a type in its own package is referenced by its bare name; write %q",
			n.Name.String(), sym)
		return
	}
	target := r.proj.Packages[pkgName]
	if target == nil {
		r.diag(n.Pos, lexer.SeverityError, CodeRefUnknownPackage,
			"package %q is not declared anywhere in the project", pkgName)
		return
	}
	if !packageHasSymbol(target, sym) {
		r.diag(n.Pos, lexer.SeverityError, CodeRefUnknownSymbol,
			"package %q has no symbol %q", pkgName, sym)
		return
	}
	// The per-package generics check skips qualified refs.
	if td := target.Types[sym]; td != nil {
		want := len(td.TypeParams)
		got := len(n.Args)
		switch {
		case want == 0 && got > 0:
			r.diag(n.Pos, lexer.SeverityError, CodeGenericNonGeneric,
				"%s.%s is not a generic type but received %d argument(s)", pkgName, sym, got)
		case want > 0 && got != want:
			r.diag(n.Pos, lexer.SeverityError, CodeGenericArity,
				"%s.%s expects %d generic argument(s), got %d", pkgName, sym, want, got)
		}
	}
}

// packageHasSymbol reports whether pkg declares sym as a type, enum, error
// or scalar.
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

// folderPkg returns the package name a folder conventionally declares: its
// last path segment.
func folderPkg(importPath string) string {
	return idents.LastSegment(importPath)
}

// isEscapingPath reports whether p is absolute or its first segment is `.`
// or `..`.
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

// diag appends a diagnostic at pos and returns a pointer into r.diags for
// setting Related; the pointer is invalid after the next append.
func (r *refResolver) diag(pos lexer.Position, sev lexer.Severity, code, format string, args ...any) *Diagnostic {
	r.diags = append(r.diags, Diagnostic{
		Pos:      pos,
		End:      pos,
		Severity: sev,
		Code:     code,
		Msg:      fmt.Sprintf(format, args...),
	})
	return &r.diags[len(r.diags)-1]
}
