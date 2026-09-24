package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// checkImports checks each file's imports: every path, and no path or alias
// repeated within one file.
func (a *analyzer) checkImports(files []*ast.File) {
	for _, f := range files {
		seenPath := map[string]*ast.Import{}
		seenAlias := map[string]*ast.Import{}
		for _, imp := range f.Imports {
			a.checkImportPath(imp)
			if prev, dup := seenPath[imp.Path]; dup {
				d := a.diag(imp.Pos, imp.Pos, lexer.SeverityError, CodeImportDuplicate,
					"duplicate import %q in this file", imp.Path)
				d.Related = related(prev.Pos, "first imported here")
				continue
			}
			seenPath[imp.Path] = imp
			alias := importAlias(imp)
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

// checkImportPath checks that imp's path is relative to the design root and
// names a design folder other than the file's own package. A qualified
// reference names a package, never an import alias.
func (a *analyzer) checkImportPath(imp *ast.Import) {
	path := imp.Path
	if path == "" {
		return
	}
	if isEscapingPath(path) {
		a.diag(imp.Pos, imp.Pos, lexer.SeverityError, CodeImportEscape,
			"import %q must be relative to the design root (no leading `/`, `./`, or `..`)", path)
		return
	}
	if root := a.opts.DesignRoot; root != "" && !folderExists(root, path) {
		a.diag(imp.Pos, imp.Pos, lexer.SeverityError, CodeImportUnresolved,
			"import %q does not match any folder under the design root", path)
		return
	}
	// A folder named after the file's own package is a self-import.
	if a.pkg.Name != "" && a.pkg.Name == idents.LastSegment(path) {
		a.diag(imp.Pos, imp.Pos, lexer.SeverityWarning, CodeImportSelf,
			"import %q resolves back into the current package %q (the files are merged anyway)",
			path, a.pkg.Name)
	}
}

// importAlias returns the name imp binds: its alias, else its path's last
// segment.
func importAlias(imp *ast.Import) string {
	if imp.Alias != "" {
		return imp.Alias
	}
	return idents.LastSegment(imp.Path)
}

// importAliasSet returns the names imps bind; nil for no imports.
func importAliasSet(imps []*ast.Import) map[string]bool {
	if len(imps) == 0 {
		return nil
	}
	out := map[string]bool{}
	for _, imp := range imps {
		if alias := importAlias(imp); alias != "" {
			out[alias] = true
		}
	}
	return out
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
