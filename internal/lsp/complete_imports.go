// Import path + package-decl LSP completions.
package lsp

import (
	"os"
	"path/filepath"
	"strings"

	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

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

// identBefore returns the identifier token immediately before t (skipping
// only whitespace, which the tokenizer has already stripped). Returns ok
// = false when the previous token is not an identifier.

func (s *Server) importPathCompletions(currentURI, currentSrc, prefix string) []protocol.CompletionItem {
	fsPath := uriToPath(currentURI)
	if fsPath == "" {
		return nil
	}
	_, _, designDir, err := config.Find(filepath.Dir(fsPath))
	if err != nil {
		return nil
	}
	currentDir, _ := filepath.Abs(filepath.Dir(fsPath))
	seen := map[string]struct{}{}
	var out []protocol.CompletionItem
	_ = filepath.WalkDir(designDir, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || !d.IsDir() {
			return nil
		}
		abs, _ := filepath.Abs(p)
		if abs == currentDir {
			return nil
		}
		// A directory only counts as an import target if it actually
		// contains a `.craftgo` source file. This filters out empty
		// nesting parents like `v1/` (when only `v1/api/foo.craftgo`
		// exists) so users see meaningful suggestions.
		entries, _ := os.ReadDir(p)
		hasCraftgo := false
		for _, e := range entries {
			if !e.IsDir() && config.IsDesignFile(e.Name()) {
				hasCraftgo = true
				break
			}
		}
		if !hasCraftgo {
			return nil
		}
		rel, err := filepath.Rel(designDir, p)
		if err != nil || rel == "." {
			return nil
		}
		// Use forward slashes - the DSL stores import paths in POSIX
		// form regardless of host OS, matching the rest of the toolchain.
		rel = filepath.ToSlash(rel)
		if _, dup := seen[rel]; dup {
			return nil
		}
		// Filter by what the user has typed inside the quotes so far.
		// Without this, `import "shared/<cursor>"` would still see
		// `users`, `orders`, etc. as suggestions because VSCode's
		// fuzzy filter does not look past the leading `/`.
		if prefix != "" && !strings.HasPrefix(rel, prefix) {
			return nil
		}
		seen[rel] = struct{}{}
		out = append(out, protocol.CompletionItem{
			Label:  rel,
			Kind:   protocol.CompletionItemKindModule,
			Detail: "package",
		})
		return nil
	})
	return out
}

// decoratorArgContext detects whether pos sits inside a `@name(…)`
// argument list and returns the decorator's bare name when it does.
// The walk is purely token-based: we step backwards from the cursor,
// tracking parenthesis depth, until we land on an opening `(` whose
// preceding tokens spell `@Ident`. A `)` along the way pops the depth
// counter - once it goes negative we have left every enclosing
// decorator and the cursor is not in an arg list.
//
// Walks include the cursor's own token (`idx`, not `idx-1`) so a
// cursor sitting exactly on the opening `(` - common right after the
// user types `@middlewares(` - still resolves cleanly. RParens are
// only counted when they're STRICTLY before the cursor; that keeps
// the closing paren of the decorator we're inside from prematurely
// flipping `depth` negative.

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
// current package's table - see [checkErrorRefs]) and they cannot
// be used as field types either, so surfacing them under a
// cross-package qualifier would offer dead-end suggestions.
func (s *Server) packageDeclCompletions(view snapshotView, currentURI, currentSrc, pkg string) []protocol.CompletionItem {
	files, root := s.projectFilesWithRoot(uriToPath(currentURI), currentSrc)
	imports := currentImports(view.file)

	// Resolve `pkg` via imports first - it might be an alias rather
	// than a literal package name. Falls back to a Package.Name match.
	targetDir := ""
	for _, imp := range imports {
		if imp == nil {
			continue
		}
		if imp.Alias == pkg {
			targetDir = importTargetDir(root, imp.Path)
			break
		}
		if imp.Alias == "" && lastPathSegment(imp.Path) == pkg {
			targetDir = importTargetDir(root, imp.Path)
			break
		}
	}

	var out []protocol.CompletionItem
	for _, p := range files {
		if p.file == nil {
			continue
		}
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

// typeCompletionsProjectWide is the type-position equivalent of
// [typeCompletions] but scoped to the entire project rather than just
// the current file. Built-in primitives are listed alongside, and
// every project-wide top-level declaration is surfaced EXCEPT
// `error` declarations: errors are domain-restricted to
// `@errors(...)` decorator args and do not resolve when used as
// field types, request bodies, etc. (see [checkLocalNamedRef] in
// the semantic phase). Surfacing them here would invite the same

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
			base := imp.Path
			for j := len(base) - 1; j >= 0; j-- {
				if base[j] == '/' {
					base = base[j+1:]
					break
				}
			}
			alias = base
		}
		if alias == "" || seen[alias] {
			continue
		}
		seen[alias] = true
		out = append(out, alias)
	}
	return out
}
