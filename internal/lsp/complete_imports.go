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

// isInsideImportString reports whether pos is inside the string of an
// `import "..."` line, after an optional alias.
func isInsideImportString(view snapshotView, pos protocol.Position) bool {
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

// importPathCompletions offers the folders under the design root that hold a
// design file, relative to the root and starting with prefix, except the buffer's.
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
		// Import paths use forward slashes on every OS.
		rel = filepath.ToSlash(rel)
		if _, dup := seen[rel]; dup {
			continue
		}
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

// quotedImportPathCompletions is [importPathCompletions] for `import |`: each
// path is inserted quoted.
func quotedImportPathCompletions(currentURI string) []protocol.CompletionItem {
	items := importPathCompletions(currentURI, "")
	for i := range items {
		items[i].InsertText = strconv.Quote(items[i].Label)
	}
	return items
}

// importStringPrefix returns the part of the `import "..."` literal between
// the opening quote and the cursor.
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
				typed := tk.Text
				if len(typed) > 0 && typed[0] == '"' {
					typed = typed[1:]
				}
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

// packageNameCompletions answers `package |` with the packages the other files
// in the folder declare or, in a folder with none, every package in the project.
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

// packageItems renders one item per name, sorted; detail may take the count
// as %d.
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

// packageDeclCompletions offers every declaration of package pkg except its
// errors, for `pkg.|`.
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
