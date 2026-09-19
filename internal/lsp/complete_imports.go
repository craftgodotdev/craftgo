// Import path + package-decl LSP completions.
package lsp

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/semantic"
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
	_, root := designProjectOf(fsPath)
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

// quotedImportPathCompletions is [importPathCompletions] for the slot
// BEFORE the quotes exist (`import <cursor>`), so each item inserts the
// literal the line needs rather than a bare path.
func quotedImportPathCompletions(currentURI string) []protocol.CompletionItem {
	items := importPathCompletions(currentURI, "")
	for i := range items {
		items[i].InsertText = strconv.Quote(items[i].Label)
	}
	return items
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

// packageNameCompletions answers `package <cursor>`. A design file's
// package is not free-form: the sibling files in its folder have already
// named it, and an import resolves a FOLDER to the package its files
// declare - so a file that disagrees with its neighbours lands its
// declarations in a package nothing imports.
//
// Siblings in the same folder are therefore the answer whenever there
// are any. In a brand-new folder there are none, and the project's other
// package names are offered instead: the flat layout, where every design
// file shares one package, is the other common shape.
func (s *Server) packageNameCompletions(currentURI, currentSrc string) []protocol.CompletionItem {
	fsPath := uriToPath(currentURI)
	v := s.loadProject(fsPath, currentSrc)
	dir := filepath.Dir(fsPath)
	siblings, project := map[string]int{}, map[string]bool{}
	for _, lf := range v.files {
		if lf.path == fsPath || lf.file == nil || lf.file.Package == nil || lf.file.Package.Name == "" {
			continue
		}
		project[lf.file.Package.Name] = true
		if filepath.Dir(lf.path) == dir {
			siblings[lf.file.Package.Name]++
		}
	}
	if len(siblings) > 0 {
		return packageItems(siblings, "declared by %d sibling file(s) in this folder")
	}
	counts := make(map[string]int, len(project))
	for name := range project {
		counts[name] = 0
	}
	return packageItems(counts, "package declared elsewhere in this project")
}

// packageItems renders one item per package name in names, sorted. The
// detail carries the count when the format string takes one.
func packageItems(names map[string]int, detail string) []protocol.CompletionItem {
	out := make([]protocol.CompletionItem, 0, len(names))
	for _, name := range sortedKeys(names) {
		d := detail
		if strings.Contains(detail, "%d") {
			d = fmt.Sprintf(detail, names[name])
		}
		out = append(out, protocol.CompletionItem{
			Label:      name,
			Kind:       protocol.CompletionItemKindModule,
			Detail:     d,
			InsertText: name,
		})
	}
	return out
}

// packageDeclCompletions returns every declaration of the package named
// pkg, for the right side of a qualified reference (`shared.<cursor>`).
//
// `error` declarations are dropped: errors are NOT cross-package
// referenceable (the `@errors(...)` resolver only looks at the current
// package's table) and they cannot be used as field types either, so
// surfacing them under a cross-package qualifier would offer dead-end
// suggestions.
func (s *Server) packageDeclCompletions(currentURI, currentSrc, pkg string) []protocol.CompletionItem {
	p := s.loadProject(uriToPath(currentURI), currentSrc).proj.Packages[pkg]
	if p == nil {
		return nil
	}
	var out []protocol.CompletionItem
	for _, d := range p.Decls(semantic.AnyDecl &^ semantic.ErrorDecls) {
		out = append(out, protocol.CompletionItem{
			Label:         d.DeclName(),
			Kind:          declSymbolKindToCompletion(d),
			Detail:        declSummary(d),
			Documentation: strings.Join(declDoc(d), "\n"),
		})
	}
	return out
}
