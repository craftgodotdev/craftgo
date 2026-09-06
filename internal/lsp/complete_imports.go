// Import path + package-decl LSP completions.
package lsp

import (
	"path/filepath"
	"strings"

	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// isInsideImportString reports whether pos lies inside an `import "…"`
// string literal - the cursor sits between the two double-quotes that
// follow an `import` keyword. We rely on token-level inspection rather
// than re-lexing the partial line because the editor may send a cursor
// position that splits a token mid-string.
func isInsideImportString(view snapshotView, pos protocol.Position) bool {
	line := int(pos.Line) + 1
	col := int(pos.Character) + 1
	for i, t := range view.tokens {
		if t.Kind != lexer.KwImport {
			continue
		}
		// Look ahead for an optional alias ident, then a String token
		// on the same logical statement.
		for j := i + 1; j < len(view.tokens) && j < i+4; j++ {
			tk := view.tokens[j]
			if tk.Kind == lexer.String {
				start := tk.Pos
				end := tk.Pos
				end.Column += len(tk.Text)
				if start.Line == line && start.Column <= col && col <= end.Column {
					return true
				}
				break
			}
			if tk.Kind != lexer.Ident {
				break
			}
		}
	}
	return false
}

// importPathCompletions returns one item per directory under the design
// root that holds at least one `.craftgo` file. Labels are the directory
// path relative to the design root, matching the literal the user is
// expected to type inside `import "…"` (e.g. `shared`, `v1/api`,
// `auth/oauth`). The current file's own directory is filtered out so
// users do not import themselves.
func importPathCompletions(currentURI, prefix string) []protocol.CompletionItem {
	fsPath := uriToPath(currentURI)
	root := designRootOf(fsPath)
	if root == "" {
		return nil
	}
	currentDir, _ := filepath.Abs(filepath.Dir(fsPath))
	seen := map[string]struct{}{}
	var out []protocol.CompletionItem
	for _, p := range designFilePaths(root) {
		dir := filepath.Dir(p)
		abs, _ := filepath.Abs(dir)
		if abs == currentDir {
			continue
		}
		rel, err := filepath.Rel(root, dir)
		if err != nil || rel == "." {
			continue
		}
		// Use forward slashes - the DSL stores import paths in POSIX
		// form regardless of host OS, matching the rest of the toolchain.
		rel = filepath.ToSlash(rel)
		if _, dup := seen[rel]; dup {
			continue
		}
		// Filter by what the user has typed inside the quotes so far.
		// Without this, `import "shared/<cursor>"` would still see
		// `users`, `orders`, etc. as suggestions because VSCode's
		// fuzzy filter does not look past the leading `/`.
		if prefix != "" && !strings.HasPrefix(rel, prefix) {
			continue
		}
		seen[rel] = struct{}{}
		out = append(out, protocol.CompletionItem{
			Label:  rel,
			Kind:   protocol.CompletionItemKindModule,
			Detail: "package",
		})
	}
	return out
}

// importStringPrefix returns the substring of the `import "…"` literal
// that lies between the opening quote and the cursor - used as the
// prefix filter for [importPathCompletions]. Returns an empty string
// when the cursor is at the very start of the literal.
func importStringPrefix(view snapshotView, pos protocol.Position) string {
	line := int(pos.Line) + 1
	col := int(pos.Character) + 1
	for i, t := range view.tokens {
		if t.Kind != lexer.KwImport {
			continue
		}
		for j := i + 1; j < len(view.tokens) && j < i+4; j++ {
			tk := view.tokens[j]
			if tk.Kind == lexer.String {
				start := tk.Pos
				if start.Line != line {
					return ""
				}
				// Token text includes both surrounding quotes - skip
				// the first.
				typed := tk.Text
				if len(typed) > 0 && typed[0] == '"' {
					typed = typed[1:]
				}
				// How many runes between the opening quote and the
				// cursor? Column-based math is OK because the lexer
				// uses 1-indexed runes.
				offset := col - (start.Column + 1)
				if offset <= 0 {
					return ""
				}
				if offset > len(typed) {
					offset = len(typed)
				}
				return typed[:offset]
			}
			if tk.Kind != lexer.Ident {
				break
			}
		}
	}
	return ""
}

// packageDeclCompletions returns every top-level declaration in the
// named sibling package, suitable for offering completion on the right
// side of a qualified reference (`shared.<cursor>` or `x.<cursor>`
// where `x` is an import alias).
//
// `error` declarations are dropped: errors are NOT cross-package
// referenceable (the `@errors(...)` resolver only looks at the
// current package's table) and they cannot
// be used as field types either, so surfacing them under a
// cross-package qualifier would offer dead-end suggestions.
func (s *Server) packageDeclCompletions(view snapshotView, currentURI, currentSrc, pkg string) []protocol.CompletionItem {
	v := s.loadProject(uriToPath(currentURI), currentSrc)
	imports := currentImports(view.file)

	// Resolve `pkg` via imports first - it might be an alias rather
	// than a literal package name. Falls back to a Package.Name match.
	targetDir := ""
	for _, imp := range imports {
		if imp == nil {
			continue
		}
		if imp.Alias == pkg {
			targetDir = importTargetDir(v.root, imp.Path)
			break
		}
		if imp.Alias == "" && idents.LastSegment(imp.Path) == pkg {
			targetDir = importTargetDir(v.root, imp.Path)
			break
		}
	}

	var out []protocol.CompletionItem
	for _, p := range v.files {
		matchByDir := targetDir != "" && inDir(p.path, targetDir)
		matchByName := p.file.Package != nil && p.file.Package.Name == pkg
		if !matchByDir && !matchByName {
			continue
		}
		for _, d := range p.file.Decls {
			if _, isError := d.(*ast.ErrorDecl); isError {
				continue
			}
			out = append(out, protocol.CompletionItem{
				Label:         d.DeclName(),
				Kind:          declSymbolKindToCompletion(d),
				Detail:        declSummary(d),
				Documentation: strings.Join(declDoc(d), "\n"),
			})
		}
	}
	return out
}

// importAliasesOf returns every alias the file's imports expose at
// the type-position level. Explicit aliases win; otherwise the
// trailing path segment becomes the implicit alias - matching the
// resolution in [findDeclAcross]. Duplicate aliases are de-duped.
func importAliasesOf(f *ast.File) []string {
	if f == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, imp := range f.Imports {
		if imp == nil {
			continue
		}
		alias := imp.Alias
		if alias == "" {
			alias = idents.LastSegment(imp.Path)
		}
		if alias == "" || seen[alias] {
			continue
		}
		seen[alias] = true
		out = append(out, alias)
	}
	return out
}
