// Project loading: the design root a buffer belongs to, walked once per
// request, every design file parsed once, and the semantic project built
// from them. Every handler that looks past the current buffer reads this
// view, so diagnostics, navigation and completion see the same project.
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
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/parser"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// loadedFile is one design file of the project: its absolute path, the
// source the analysis saw (the editor buffer when the file is open, the
// disk copy otherwise), its token stream and its parsed AST.
type loadedFile struct {
	path   string
	src    string
	tokens []lexer.Token
	file   *ast.File
}

// projectView is the project a buffer belongs to. Outside a project (no
// manifest above the buffer) root is empty and files holds the buffer
// alone, so single-file editing keeps every feature working.
type projectView struct {
	root    string
	current string // path of the buffer the view was built for
	files   []loadedFile
	proj    *semantic.Project
	diags   []lexer.Diagnostic
}

// loadProject builds the view for the buffer at fsPath holding src. In a
// project every design file under the root is loaded, and a file without
// a `package` declaration is assigned its folder's name so the analysis
// can place it. fsPath is empty for an untitled buffer.
func (s *Server) loadProject(fsPath, src string) projectView {
	v := projectView{root: designRootOf(fsPath), current: fsPath}
	if v.root == "" {
		v.files = []loadedFile{{path: fsPath, src: src}}
	} else {
		v.files = s.designFiles(v.root, fsPath, src)
	}
	asts := make([]*ast.File, 0, len(v.files))
	for i := range v.files {
		lf := &v.files[i]
		p := parser.New(lf.path, lf.src)
		lf.file = p.Parse()
		lf.tokens = p.Tokens()
		v.diags = append(v.diags, p.Diagnostics()...)
		if v.root != "" {
			if lf.file.Package == nil {
				lf.file.Package = &ast.PackageDecl{Name: filepath.Base(filepath.Dir(lf.path))}
			}
			lf.file.Package.Pos.Filename = lf.path
		}
		asts = append(asts, lf.file)
	}
	var diags []semantic.Diagnostic
	v.proj, diags = semantic.AnalyzeProject(asts, semantic.Options{DesignRoot: v.root})
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

// lookup resolves name (bare or `pkg.Name`) to a declaration of the
// selected kinds as seen from the buffer's package.
func (v projectView) lookup(name string, kinds semantic.DeclKind) ast.Decl {
	return v.proj.Lookup(v.currentPackage(), name, kinds)
}

// locationOf returns the LSP location of the n-column span at pos. A span
// inside the buffer itself reports the editor's own URI, so an untitled
// or non-file buffer still gets a usable location.
func (v projectView) locationOf(pos lexer.Position, n int, current protocol.DocumentURI) protocol.Location {
	u := current
	if pos.Filename != v.current {
		u = uri.New(pathToFileURIString(pos.Filename))
	}
	return protocol.Location{URI: u, Range: rangeOfPosLen(pos, n)}
}

// sortedKeys returns the keys of m in alphabetical order.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// designRootOf returns the design root of the project containing fsPath,
// or "" when fsPath is empty or no manifest is found above it.
func designRootOf(fsPath string) string {
	if fsPath == "" {
		return ""
	}
	_, _, root, err := config.Find(filepath.Dir(fsPath))
	if err != nil {
		return ""
	}
	return root
}

// designFilePaths lists every design file under root in walk order.
func designFilePaths(root string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && config.IsDesignFile(p) {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// designFiles reads every design file under root. The buffer at fsPath
// (src) and every other open buffer take precedence over the disk copy,
// and open buffers under root that the walk did not find (deleted or not
// yet saved while open) are appended so they still take part.
func (s *Server) designFiles(root, fsPath, src string) []loadedFile {
	seen := map[string]bool{}
	var out []loadedFile
	for _, p := range designFilePaths(root) {
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

// readFile returns the source of path: currentSrc when path is the buffer
// being served, the editor's copy when the file is open, the disk copy
// otherwise ("" when unreadable).
func (s *Server) readFile(path, currentPath, currentSrc string) string {
	if path == currentPath {
		return currentSrc
	}
	if cached := s.snapshot(uri.New(pathToURI(path))); cached != "" {
		return cached
	}
	if data, err := os.ReadFile(path); err == nil {
		return string(data)
	}
	return ""
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
