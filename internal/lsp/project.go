package lsp

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/designopts"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// loadedFile is one design file of a project: its path and its parse of the
// text analysed, the open buffer over the disk copy.
type loadedFile struct {
	path string
	snapshotView
}

// projectView is the analysed project of a buffer. Outside a project (no
// manifest above the buffer) root is empty and files holds the buffer alone.
type projectView struct {
	root    string
	current string // path of the buffer the view was built for
	files   []loadedFile
	proj    *semantic.Project
	diags   []lexer.Diagnostic
}

// loadProject parses and analyses the project of the buffer at fsPath (empty
// for an untitled buffer) holding src, with the manifest's options.
func (s *server) loadProject(fsPath, src string) projectView {
	cfg, root := designopts.ProjectOf(fsPath)
	srcs := []designopts.Source{{Path: fsPath, Text: src}}
	if root != "" {
		srcs = s.designSources(root, fsPath, src)
	}
	proj, parsed, diags := designopts.Analyze(srcs, root, cfg)
	v := projectView{root: root, current: fsPath, files: make([]loadedFile, len(srcs)), proj: proj, diags: diags}
	for i, in := range srcs {
		v.files[i] = loadedFile{path: in.Path, snapshotView: snapshotView{src: in.Text, tokens: parsed[i].Tokens, file: parsed[i].File}}
	}
	return v
}

// buffer returns the parse of the buffer the view was built for.
func (v projectView) buffer() snapshotView {
	for _, lf := range v.files {
		if lf.path == v.current {
			return lf.snapshotView
		}
	}
	return snapshotView{}
}

// currentPackage returns the package name of the buffer the view was
// built for, "" when it declares none.
func (v projectView) currentPackage() string {
	return v.buffer().packageName()
}

// lookup resolves name (bare or `pkg.Name`) to a declaration of the
// selected kinds as seen from the buffer's package.
func (v projectView) lookup(name string, kinds semantic.DeclKind) ast.Decl {
	return v.proj.Lookup(v.currentPackage(), name, kinds)
}

// locationOf returns the location of the n bytes at pos.
func (v projectView) locationOf(pos lexer.Position, n int, current protocol.DocumentURI) protocol.Location {
	return protocol.Location{URI: v.uriOf(pos.Filename, current), Range: spanRange(v.srcOf(pos.Filename), pos, n)}
}

// uriOf returns the URI of the file at path; the buffer keeps the editor's
// URI, current, so an untitled buffer still has one.
func (v projectView) uriOf(path string, current protocol.DocumentURI) protocol.DocumentURI {
	if path == v.current {
		return current
	}
	return uri.File(path)
}

// srcOf returns the text analysed for the file at path.
func (v projectView) srcOf(path string) string {
	for _, lf := range v.files {
		if lf.path == path {
			return lf.src
		}
	}
	return ""
}

// designSources reads every design file under root, open buffers (src for
// fsPath) over disk, and appends the buffer at fsPath and the open buffers
// under root the walk did not find.
func (s *server) designSources(root, fsPath, src string) []designopts.Source {
	open := s.openFiles()
	if fsPath != "" {
		open[fsPath] = src
	}
	seen := map[string]bool{}
	var out []designopts.Source
	for _, p := range designopts.FilesBestEffort(root) {
		seen[p] = true
		out = append(out, designopts.Source{Path: p, Text: readFile(p, open)})
	}
	var extra []designopts.Source
	for p, text := range open {
		if !seen[p] && (p == fsPath || config.IsDesignFile(p) && isUnderDesignRoot(p, root)) {
			extra = append(extra, designopts.Source{Path: p, Text: text})
		}
	}
	sort.Slice(extra, func(i, j int) bool { return extra[i].Path < extra[j].Path })
	return append(out, extra...)
}

// readFile returns the text of the file at path: its open buffer, else the
// disk copy ("" when unreadable).
func readFile(path string, open map[string]string) string {
	if text, ok := open[path]; ok {
		return text
	}
	if data, err := os.ReadFile(path); err == nil {
		return string(data)
	}
	return ""
}

// isUnderDesignRoot reports whether p is dir or inside it; `/proj/design2` is
// not inside `/proj/design`.
func isUnderDesignRoot(p, dir string) bool {
	if dir == "" {
		return false
	}
	return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator))
}
