// Project-wide lookup: walking the design root for sibling files, and
// finding a declaration across files (kind-aware, import-scoped).
package lsp

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/parser"
)

// projectAST is one parsed `.craftgo` file collected from a design root.
// path is the absolute filesystem path; file is the parser output (may
// be a partial AST if the parse hit recoverable errors).
type projectAST struct {
	path string
	file *ast.File
}

// projectASTs walks upward from currentPath to find a craftgo project
// root, then parses every `.craftgo` file beneath it. The current
// buffer's text is preferred over its on-disk content so unsaved edits
// are reflected in cross-file lookups (hover, go-to-def, references).
//
// Returns nil when no project root can be found - callers should
// fall back to single-file behaviour in that case.
func (s *Server) projectASTs(currentPath, currentSrc string) []projectAST {
	files, _ := s.projectFilesWithRoot(currentPath, currentSrc)
	return files
}

// projectFilesWithRoot is the alias-aware variant of [projectASTs] that
// also returns the design-root directory. Callers that need to resolve
// `import alias "from/x/y"` paths back to filesystem locations require
// the root, so this is the canonical entry point for hover / def /
// references; [projectASTs] is kept as a thin wrapper for callers that
// only need the file list.
func (s *Server) projectFilesWithRoot(currentPath, currentSrc string) ([]projectAST, string) {
	if currentPath == "" {
		return nil, ""
	}
	_, _, designDir, err := config.Find(filepath.Dir(currentPath))
	if err != nil {
		return nil, ""
	}
	var out []projectAST
	_ = filepath.WalkDir(designDir, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil
		}
		if !config.IsDesignFile(p) {
			return nil
		}
		src := s.readFile(p, currentPath, currentSrc)
		if src == "" {
			return nil
		}
		f := parser.New(p, src).Parse()
		out = append(out, projectAST{path: p, file: f})
		return nil
	})
	return out, designDir
}

// findDecl returns the first top-level declaration whose declared name
// matches. Cross-package lookups are not handled here - the caller can
// inspect the import list separately if needed.
func findDecl(f *ast.File, name string) ast.Decl {
	if f == nil {
		return nil
	}
	for _, d := range f.Decls {
		if d.DeclName() == name {
			return d
		}
	}
	return nil
}

// findDeclKindAware returns the in-file decl whose name matches and whose
// kind is appropriate for the supplied lookup context. The semantic layer
// keeps middleware names in a separate namespace from types/enums/errors/
// scalars, so a name like `AuthRequired` can legally be both an `error`
// AND a `middleware` decl. Without context, a click in
// `@middlewares(AuthRequired)` would land on whichever decl came first
// in source order - this helper restricts the search by the kind the
// surrounding syntax is documented to accept.
//
// ctx values:
//   - "middlewares": cursor inside `@middlewares(...)` - return MiddlewareDecl
//   - "errors":      cursor inside `@errors(...)`      - return ErrorDecl
//   - "type":        cursor in a type / mixin / field-type / request /
//     response / generic-arg position - return anything EXCEPT
//     MiddlewareDecl (the only decl kind that does not appear in
//     type-shape positions). This handles the inverse ambiguity:
//     `type X { AuthRequired }` should not jump to a middleware decl.
//   - "":            unknown context - returns nil so the caller falls
//     back to the generic [findDecl].
//
// Returns nil when no matching decl of the right kind exists in this
// file; callers should fall back to the generic [findDecl].
func findDeclKindAware(f *ast.File, name, ctx string) ast.Decl {
	if f == nil || ctx == "" {
		return nil
	}
	matches := makeKindMatcher(ctx)
	for _, d := range f.Decls {
		if d.DeclName() == name && matches(d) {
			return d
		}
	}
	return nil
}

// findDeclAcross resolves a (possibly qualified) name against a set of
// already-parsed project files. The qualifier matches in priority order:
//
//  1. An alias from currentImports (`import x "from/x/y/z"` → `x.Type`).
//  2. The implicit alias derived from the last segment of an unaliased
//     import path (`import "from/x/y/z"` → `z.Type`).
//  3. A direct `package` declaration with the same name (the simple
//     `import "users"` case where path == package name).
//
// designRoot is needed so alias resolution can map an import path
// (relative to the design folder) back to the on-disk directory whose
// files we should search. Pass empty string when the lookup is
// in-package only and bare names are sufficient.
func findDeclAcross(files []projectAST, name string, currentImports []*ast.Import, designRoot string) (ast.Decl, projectAST, bool) {
	return findDeclAcrossKindAware(files, name, currentImports, designRoot, "")
}

// findDeclAcrossKindAware mirrors [findDeclAcross] with the additional
// kind filter consumed by [findDeclKindAware]. Pass ctx="" to disable
// the filter (legacy behaviour); pass "middlewares" / "errors" / "type"
// to restrict matches to the expected decl kind. The filter is essential
// when the same name lives in two decl namespaces across the project
// (e.g. `middleware AuthRequired` in one file, `error AuthRequired` in
// another) - without it the linear scan returns whichever appeared
// first, which is wrong for a click in `@middlewares(...)`.
func findDeclAcrossKindAware(files []projectAST, name string, currentImports []*ast.Import, designRoot, ctx string) (ast.Decl, projectAST, bool) {
	pkgQualifier := ""
	bare := name
	for i := 0; i < len(name); i++ {
		if name[i] == '.' {
			pkgQualifier = name[:i]
			bare = name[i+1:]
			break
		}
	}
	kindMatch := makeKindMatcher(ctx)
	if pkgQualifier == "" {
		for _, p := range files {
			if p.file == nil {
				continue
			}
			for _, d := range p.file.Decls {
				if d.DeclName() == bare && kindMatch(d) {
					return d, p, true
				}
			}
		}
		return nil, projectAST{}, false
	}
	// Alias-based resolution: walk the current file's imports and
	// pick whichever alias (explicit or implicit) matches the
	// qualifier. The matched import's Path tells us which directory
	// to search.
	targetDir := ""
	for _, imp := range currentImports {
		if imp == nil {
			continue
		}
		if imp.Alias == pkgQualifier {
			targetDir = importTargetDir(designRoot, imp.Path)
			break
		}
		if imp.Alias == "" && idents.LastSegment(imp.Path) == pkgQualifier {
			targetDir = importTargetDir(designRoot, imp.Path)
			break
		}
	}
	if targetDir != "" {
		for _, p := range files {
			if !inDir(p.path, targetDir) {
				continue
			}
			if p.file == nil {
				continue
			}
			for _, d := range p.file.Decls {
				if d.DeclName() == bare && kindMatch(d) {
					return d, p, true
				}
			}
		}
	}
	// Fallback: match by Package declaration. Handles the simple
	// `import "users"` case where the path equals the package name
	// AND files outside the project (no design root) but in the same
	// AST set.
	for _, p := range files {
		if p.file == nil || p.file.Package == nil || p.file.Package.Name != pkgQualifier {
			continue
		}
		for _, d := range p.file.Decls {
			if d.DeclName() == bare && kindMatch(d) {
				return d, p, true
			}
		}
	}
	return nil, projectAST{}, false
}

// makeKindMatcher returns a predicate that tests whether a decl matches
// the lookup context ctx. The empty context accepts every decl - that
// is how legacy callers (no context-aware lookup) pass through.
func makeKindMatcher(ctx string) func(ast.Decl) bool {
	switch ctx {
	case "middlewares":
		return func(d ast.Decl) bool {
			_, ok := d.(*ast.MiddlewareDecl)
			return ok
		}
	case "errors":
		return func(d ast.Decl) bool {
			_, ok := d.(*ast.ErrorDecl)
			return ok
		}
	case "type":
		return func(d ast.Decl) bool {
			_, isMW := d.(*ast.MiddlewareDecl)
			return !isMW
		}
	default:
		return func(ast.Decl) bool { return true }
	}
}

// importTargetDir resolves an import path (slash-separated, relative to
// the design root) to an absolute filesystem directory. Returns empty
// when designRoot is unset.
func importTargetDir(designRoot, importPath string) string {
	if designRoot == "" || importPath == "" {
		return ""
	}
	return filepath.Join(designRoot, filepath.FromSlash(importPath))
}

// inDir reports whether path lies directly inside (or equals) dir. The
// comparison is path-aware so `/proj/users` does NOT match a target of
// `/proj/use`.
func inDir(path, dir string) bool {
	clean := filepath.Clean(path)
	dirClean := filepath.Clean(dir)
	return filepath.Dir(clean) == dirClean
}

// isUnderDesignRoot reports whether file path p lives inside dir,
// requiring a path-separator boundary after the prefix so a sibling like
// `/proj/design2` or `/proj/design_backup` does NOT match the design root
// `/proj/design` (which a bare strings.HasPrefix would).
func isUnderDesignRoot(p, dir string) bool {
	if dir == "" {
		return false
	}
	return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator))
}
