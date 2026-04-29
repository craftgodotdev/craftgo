package lsp

import (
	"os"
	"path/filepath"

	"go.lsp.dev/protocol"

	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/config"
	"github.com/dropship-dev/craftgo/internal/lexer"
	"github.com/dropship-dev/craftgo/internal/parser"
)

// snapshot is the shared parse view that every feature handler operates
// on. Re-tokenising and re-parsing on every request keeps the wire model
// simple and matches what `craftgo lint` would see — no risk of stale
// AST drift between editor and CLI.
type snapshotView struct {
	src    string
	tokens []lexer.Token
	file   *ast.File
}

func parseSnapshot(filename, src string) snapshotView {
	toks := lexer.New(filename, src).Tokenize()
	f := parser.New(filename, src).Parse()
	return snapshotView{src: src, tokens: toks, file: f}
}

// tokenAt returns the token whose source span covers the supplied
// LSP-style (0-indexed) cursor and the index into the token slice. The
// hit token may be EOF for cursors past the last real token; callers
// should check Kind to filter that out.
func (v snapshotView) tokenAt(line, character uint32) (int, lexer.Token) {
	target := lexer.Position{Line: int(line) + 1, Column: int(character) + 1}
	best := -1
	for i, t := range v.tokens {
		if t.Kind == lexer.EOF {
			continue
		}
		if t.Pos.Line != target.Line {
			continue
		}
		end := t.Pos.Column + len(t.Text)
		if t.Pos.Column <= target.Column && target.Column <= end {
			best = i
		}
	}
	if best < 0 {
		return -1, lexer.Token{}
	}
	return best, v.tokens[best]
}

// rangeOf returns the LSP range covering t.
func rangeOf(t lexer.Token) protocol.Range {
	start := lspPos(t.Pos)
	endPos := t.Pos
	endPos.Column += len(t.Text)
	return protocol.Range{Start: start, End: lspPos(endPos)}
}

// rangeOfPosLen builds a range starting at p with width n columns.
func rangeOfPosLen(p lexer.Position, n int) protocol.Range {
	start := lspPos(p)
	endPos := p
	endPos.Column += n
	return protocol.Range{Start: start, End: lspPos(endPos)}
}

// findDecl returns the first top-level declaration whose declared name
// matches. Cross-package lookups are not handled here — the caller can
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
// Returns nil when no project root can be found — callers should
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
		if filepath.Ext(p) != ".craftgo" {
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
	pkgQualifier := ""
	bare := name
	for i := 0; i < len(name); i++ {
		if name[i] == '.' {
			pkgQualifier = name[:i]
			bare = name[i+1:]
			break
		}
	}
	if pkgQualifier == "" {
		for _, p := range files {
			if p.file == nil {
				continue
			}
			for _, d := range p.file.Decls {
				if d.DeclName() == bare {
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
		if imp.Alias == "" && lastPathSegment(imp.Path) == pkgQualifier {
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
				if d.DeclName() == bare {
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
			if d.DeclName() == bare {
				return d, p, true
			}
		}
	}
	return nil, projectAST{}, false
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

// lastPathSegment returns the trailing component of a slash-separated
// import path. Used as the implicit alias when an import has no
// explicit `alias` keyword.
func lastPathSegment(p string) string {
	if i := lastIndex(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

func lastIndex(s string, b byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// inDir reports whether path lies directly inside (or equals) dir. The
// comparison is path-aware so `/proj/users` does NOT match a target of
// `/proj/use`.
func inDir(path, dir string) bool {
	clean := filepath.Clean(path)
	dirClean := filepath.Clean(dir)
	if filepath.Dir(clean) == dirClean {
		return true
	}
	return false
}

// pathToFileURIString builds a `file://...` URI string from an absolute
// filesystem path. Feature handlers feed the result into [uri.New] so
// the typed URI lines up with what the editor would have sent for a
// sibling file in the same project.
func pathToFileURIString(path string) string {
	if path == "" {
		return ""
	}
	abs := path
	if !filepath.IsAbs(abs) {
		if a, err := filepath.Abs(abs); err == nil {
			abs = a
		}
	}
	return "file://" + abs
}

// declSummary renders a short one-line signature for d, suitable for the
// header of a hover popup. It mirrors the canonical formatter style so
// editors and `craftgo fmt` agree on what the construct looks like.
func declSummary(d ast.Decl) string {
	switch v := d.(type) {
	case *ast.TypeDecl:
		s := "type " + v.Name
		if len(v.TypeParams) > 0 {
			s += "<"
			for i, tp := range v.TypeParams {
				if i > 0 {
					s += ", "
				}
				s += tp
			}
			s += ">"
		}
		return s
	case *ast.EnumDecl:
		return "enum " + v.Name
	case *ast.ErrorDecl:
		return "error " + v.Category + " " + v.Name
	case *ast.ScalarDecl:
		return "scalar " + v.Name + " " + v.Primitive
	case *ast.MiddlewareDecl:
		return "middleware " + v.Name
	case *ast.ServiceDecl:
		if v.Extend {
			return "extend service " + v.Name
		}
		return "service " + v.Name
	}
	return ""
}

// declDoc returns the doc-comment lines of d, or nil for declarations
// that do not carry doc strings (Scalar / Middleware).
func declDoc(d ast.Decl) []string {
	switch v := d.(type) {
	case *ast.TypeDecl:
		return v.Doc
	case *ast.EnumDecl:
		return v.Doc
	case *ast.ErrorDecl:
		return v.Doc
	case *ast.ServiceDecl:
		return v.Doc
	}
	return nil
}
