package lsp

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/config"
	"github.com/dropship-dev/craftgo/internal/lexer"
	"github.com/dropship-dev/craftgo/internal/parser"
	"github.com/dropship-dev/craftgo/internal/semantic"
)

// buildDiagnostics runs the parser and the semantic analyser over src
// (and, when the file lives inside a design root, every sibling
// `.craftgo` file in the same project) so that cross-package qualified
// references like `users.UserRef` resolve correctly. The returned slice
// contains only diagnostics whose source position belongs to filename —
// sibling errors are folded into the publishDiagnostics for THAT file
// when it is opened separately.
//
// When filename is not part of a discoverable project layout (no
// `craftgo.design.yaml` walking upward), the analyser falls back to
// single-file mode. Cross-package refs in such files will be reported
// as `decorator/ref` errors, which matches CLI behaviour.
func (s *Server) buildDiagnostics(u uri.URI, src string) []protocol.Diagnostic {
	fsPath := uriToPath(string(u))
	files, designRoot := s.collectProjectFiles(fsPath, src)

	var diags []lexer.Diagnostic
	singleFile := len(files) == 0
	if singleFile {
		// Fallback: parse just the buffer on its own. Use the resolved
		// fs path when available so the per-file filter below sees a
		// matching Pos.Filename; otherwise tag with the URI itself so
		// the filter is a no-op.
		fname := fsPath
		if fname == "" {
			fname = string(u)
		}
		p := parser.New(fname, src)
		f := p.Parse()
		diags = append(diags, p.Diagnostics()...)
		if f != nil {
			_, sd := semantic.Analyze([]*ast.File{f})
			diags = append(diags, sd...)
		}
	} else {
		// Project-wide: parse every file, then AnalyzeProject so cross-pkg
		// qualified refs resolve.
		var astFiles []*ast.File
		for _, e := range files {
			p := parser.New(e.path, e.src)
			f := p.Parse()
			diags = append(diags, p.Diagnostics()...)
			if f != nil {
				if f.Package == nil {
					f.Package = &ast.PackageDecl{Name: filepath.Base(filepath.Dir(e.path))}
				}
				f.Package.Pos.Filename = e.path
				astFiles = append(astFiles, f)
			}
		}
		_, semDiags := semantic.AnalyzeProject(astFiles, semantic.Options{DesignRoot: designRoot})
		diags = append(diags, semDiags...)
	}

	target := strings.TrimSpace(fsPath)
	out := make([]protocol.Diagnostic, 0, len(diags))
	seen := map[string]bool{}
	for _, d := range diags {
		// Filter to current file ONLY when project mode is active; in
		// single-file fallback the parser already tagged everything
		// with our chosen filename, and accepting blank Filename keeps
		// us robust to phases that omit it.
		if !singleFile && target != "" && d.Pos.Filename != "" && d.Pos.Filename != target {
			continue
		}
		key := keyOf(d)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, toLSP(d))
	}
	return out
}

type projectFile struct {
	path string
	src  string
}

// collectProjectFiles returns every `.craftgo` file in the same design
// root as fsPath plus the editor-cached current buffer override. When no
// design root exists (or fsPath is empty), it returns nil and the caller
// falls back to single-file analysis.
func (s *Server) collectProjectFiles(fsPath, currentSrc string) ([]projectFile, string) {
	if fsPath == "" {
		return nil, ""
	}
	_, _, designDir, err := config.Find(filepath.Dir(fsPath))
	if err != nil {
		return nil, ""
	}
	var out []projectFile
	_ = filepath.WalkDir(designDir, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil
		}
		if filepath.Ext(p) != ".craftgo" {
			return nil
		}
		out = append(out, projectFile{path: p, src: s.readFile(p, fsPath, currentSrc)})
		return nil
	})
	return out, designDir
}

// readFile prefers the editor-cached buffer (so unsaved edits are
// reflected in cross-file analysis) and otherwise reads from disk. The
// path argument is the project-relative file we are about to read; the
// fsPath/currentSrc pair lets the caller pass the buffer it was
// validating in case it has not yet been pushed back into the cache.
func (s *Server) readFile(path, currentPath, currentSrc string) string {
	if path == currentPath {
		return currentSrc
	}
	// Fast path: maybe another buffer is open for this same path.
	candidate := uri.New(pathToURI(path))
	if cached := s.snapshot(candidate); cached != "" {
		return cached
	}
	if data, err := os.ReadFile(path); err == nil {
		return string(data)
	}
	return ""
}

// uriToPath converts an `lsp` document URI string to a filesystem path.
// Empty when the URI is not a `file://` URL — which currently rules out
// untitled buffers from project-wide analysis.
func uriToPath(u string) string {
	if u == "" {
		return ""
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return ""
	}
	if parsed.Scheme != "file" {
		return ""
	}
	return parsed.Path
}

// pathToURI is the inverse helper, used to build URIs that line up with
// what the editor would have sent for a sibling file.
func pathToURI(p string) string {
	if filepath.IsAbs(p) {
		return "file://" + p
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "file://" + p
	}
	return "file://" + abs
}

// keyOf collapses identical diagnostics that some passes may emit
// twice (a parser error followed by the semantic phase complaining
// about the same span). Without this guard the editor would render
// stacked duplicate squigglies.
func keyOf(d lexer.Diagnostic) string {
	return d.Pos.Filename + ":" + intToA(d.Pos.Line) + ":" + intToA(d.Pos.Column) + ":" + d.Code + ":" + d.Msg
}

func intToA(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// toLSP converts an internal [lexer.Diagnostic] to the LSP wire shape.
// The Range degenerates to a zero-length cursor when End is the zero
// position — every diagnostic still gets a clickable location that way.
func toLSP(d lexer.Diagnostic) protocol.Diagnostic {
	end := d.End
	if !end.IsValid() {
		end = d.Pos
	}
	return protocol.Diagnostic{
		Range: protocol.Range{
			Start: lspPos(d.Pos),
			End:   lspPos(end),
		},
		Severity: lspSeverity(d.Severity),
		Code:     d.Code,
		Source:   "craftgo",
		Message:  d.Msg,
	}
}

func lspPos(p lexer.Position) protocol.Position {
	line, col := p.Line, p.Column
	if line < 1 {
		line = 1
	}
	if col < 1 {
		col = 1
	}
	return protocol.Position{
		Line:      uint32(line - 1),
		Character: uint32(col - 1),
	}
}

func lspSeverity(s lexer.Severity) protocol.DiagnosticSeverity {
	switch s {
	case lexer.SeverityError:
		return protocol.DiagnosticSeverityError
	case lexer.SeverityWarning:
		return protocol.DiagnosticSeverityWarning
	case lexer.SeverityInfo:
		return protocol.DiagnosticSeverityInformation
	case lexer.SeverityHint:
		return protocol.DiagnosticSeverityHint
	default:
		return protocol.DiagnosticSeverityError
	}
}
