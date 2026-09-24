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

// loadedFile is one design file: its path, the text analysed (the open buffer
// over the disk copy), its tokens and its AST.
type loadedFile struct {
	path   string
	src    string
	tokens []lexer.Token
	file   *ast.File
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
	cfg, root := designProjectOf(fsPath)
	v := projectView{root: root, current: fsPath}

	var srcs []designopts.Source
	if v.root == "" {
		v.files = []loadedFile{{path: fsPath, src: src}}
	} else {
		v.files = s.designFiles(v.root, fsPath, src)
	}
	for _, lf := range v.files {
		srcs = append(srcs, designopts.Source{Path: lf.path, Text: lf.src})
	}

	parsed, parseDiags := designopts.Parse(srcs)
	v.diags = append(v.diags, parseDiags...)
	for i := range v.files {
		v.files[i].file = parsed[i].File
		v.files[i].tokens = parsed[i].Tokens
	}

	var diags []semantic.Diagnostic
	v.proj, diags = semantic.AnalyzeProject(designopts.ASTs(parsed), designopts.For(v.root, cfg))
	v.diags = append(v.diags, diags...)
	return v
}

// currentPackage returns the package name of the buffer the view was
// built for ("" when, outside a project, it declares none).
func (v projectView) currentPackage() string {
	for _, lf := range v.files {
		if lf.path == v.current && lf.file.Package != nil {
			return lf.file.Package.Name
		}
	}
	return ""
}

// hasErrors reports whether the buffer's own or untagged diagnostics hold an
// error; a warning does not count.
func (v projectView) hasErrors() bool {
	for _, d := range v.diags {
		if d.IsError() && (d.Pos.Filename == v.current || d.Pos.Filename == "") {
			return true
		}
	}
	return false
}

// lookup resolves name (bare or `pkg.Name`) to a declaration of the
// selected kinds as seen from the buffer's package.
func (v projectView) lookup(name string, kinds semantic.DeclKind) ast.Decl {
	return v.proj.Lookup(v.currentPackage(), name, kinds)
}

// locationOf returns the location of the n-column span at pos; a span in the
// buffer keeps the editor's URI, so an untitled buffer still gets one.
func (v projectView) locationOf(pos lexer.Position, n int, current protocol.DocumentURI) protocol.Location {
	u := current
	if pos.Filename != v.current {
		u = uri.File(pos.Filename)
	}
	return protocol.Location{URI: u, Range: rangeOfPosLen(pos, n)}
}

// designProjectOf returns the manifest and design root of the project holding
// fsPath; both are zero when fsPath is empty or no manifest is found above it.
func designProjectOf(fsPath string) (*config.Config, string) {
	if fsPath == "" {
		return nil, ""
	}
	cfg, _, root, err := config.Find(filepath.Dir(fsPath))
	if err != nil {
		return nil, ""
	}
	return cfg, root
}

// designFiles reads every design file under root, open buffers (src for fsPath)
// over disk, and appends the open buffers under root the walk did not find.
func (s *server) designFiles(root, fsPath, src string) []loadedFile {
	seen := map[string]bool{}
	var out []loadedFile
	for _, p := range designopts.FilesBestEffort(root) {
		seen[p] = true
		out = append(out, loadedFile{path: p, src: s.readFile(p, fsPath, src)})
	}
	var extra []loadedFile
	if fsPath != "" && !seen[fsPath] {
		seen[fsPath] = true
		extra = append(extra, loadedFile{path: fsPath, src: src})
	}
	for u := range s.openDocURIs() {
		p := uriToPath(string(u))
		if p == "" || seen[p] || !config.IsDesignFile(p) || !isUnderDesignRoot(p, root) {
			continue
		}
		seen[p] = true
		extra = append(extra, loadedFile{path: p, src: s.snapshot(u)})
	}
	sort.Slice(extra, func(i, j int) bool { return extra[i].path < extra[j].path })
	return append(out, extra...)
}

// readFile returns the text of path: currentSrc for currentPath, else the open
// buffer, else the disk copy ("" when unreadable).
func (s *server) readFile(path, currentPath, currentSrc string) string {
	if path == currentPath {
		return currentSrc
	}
	if cached := s.snapshot(uri.File(path)); cached != "" {
		return cached
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
