package lsp

import (
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// buildDiagnostics runs the parser and the semantic analyser over src
// (and, when the file lives inside a design root, every sibling
// `.craftgo` file in the same project) so that cross-package qualified
// references like `users.UserRef` resolve correctly. The returned slice
// contains only diagnostics whose source position belongs to filename -
// sibling errors are folded into the publishDiagnostics for THAT file
// when it is opened separately.
//
// When filename is not part of a discoverable project layout (no
// `craftgo.design.yaml` walking upward), the analyser falls back to
// single-file mode. Cross-package refs in such files will be reported
// as `decorator/ref` errors, which matches CLI behaviour.
func (s *Server) buildDiagnostics(u uri.URI, src string) []protocol.Diagnostic {
	perFile, _ := s.buildProjectDiagnostics(u, src)
	target := strings.TrimSpace(uriToPath(string(u)))
	// In project mode we partition by source filename and the caller
	// only wants the triggering file's slice. In single-file fallback
	// (no resolvable target) the perFile map uses the URI string as
	// the key and the partition is whole-file - either way the lookup
	// hits the right slot.
	if target == "" {
		// Untitled buffer or non-file URI: fold every bucket into one
		// since there's no meaningful Pos.Filename to filter on.
		var out []protocol.Diagnostic
		for _, v := range perFile {
			out = append(out, v...)
		}
		return out
	}
	return perFile[target]
}

// buildProjectDiagnostics loads and analyses the buffer's project once and
// partitions the resulting diagnostics by source filename so the caller
// can publish per-file lists. Returns (perFile, designRoot); designRoot
// is empty when the file is outside any discoverable project.
//
// Used by [Server.publishDiagnostics] to refresh sibling files whose
// diagnostics may have changed because of an edit elsewhere in the
// project (e.g. adding a field to a request type clears the
// "path segment has no matching field" error in the service file).
// Also feeds [Server.buildDiagnostics] - the single-file accessor.
func (s *Server) buildProjectDiagnostics(u uri.URI, src string) (map[string][]protocol.Diagnostic, string) {
	fsPath := uriToPath(string(u))
	v := s.loadProject(fsPath, src)

	// Source text per file, so toLSP can place diagnostics on UTF-16
	// columns. Untagged diagnostics (empty Filename) are bucketed under
	// fsPath and read the editor buffer.
	srcByFile := make(map[string]string, len(v.files)+1)
	for _, lf := range v.files {
		srcByFile[lf.path] = lf.src
	}
	srcByFile[""] = src

	// Partition by source file. Diagnostics with empty Filename land in
	// the bucket for the triggering URI - they came from a phase that
	// didn't tag a span (e.g. single-file fallback emits some without).
	perFile := map[string][]protocol.Diagnostic{}
	seen := map[string]map[string]bool{}
	for _, d := range v.diags {
		key := d.Pos.Filename
		if key == "" {
			key = fsPath
		}
		if seen[key] == nil {
			seen[key] = map[string]bool{}
		}
		k := keyOf(d)
		if seen[key][k] {
			continue
		}
		seen[key][k] = true
		perFile[key] = append(perFile[key], toLSP(d, srcByFile))
	}
	// Ensure the triggering file has at least an empty slice so the
	// publisher always sends a clear-diagnostics notification for it,
	// even if nothing came back.
	if _, ok := perFile[fsPath]; !ok && fsPath != "" {
		perFile[fsPath] = []protocol.Diagnostic{}
	}
	return perFile, v.root
}

// uriToPath converts an `lsp` document URI string to a filesystem path.
// Empty when the URI is not a `file://` URL - which currently rules out
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
	p := parsed.Path
	// A Windows file URI (file:///C:/x) parses to "/C:/x"; drop the leading
	// slash before the drive letter so filepath sees a valid "C:/x", then
	// switch to OS-native separators. No-op for POSIX paths ("/home/x").
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}

// pathToURI is the inverse helper, used to build URIs that line up with
// what the editor would have sent for a sibling file.
func pathToURI(p string) string {
	return string(uri.File(p))
}

// keyOf collapses identical diagnostics that some passes may emit
// twice (a parser error followed by the semantic phase complaining
// about the same span). Without this guard the editor would render
// stacked duplicate squigglies.
func keyOf(d lexer.Diagnostic) string {
	return d.Pos.Filename + ":" + strconv.Itoa(d.Pos.Line) + ":" + strconv.Itoa(d.Pos.Column) + ":" + d.Code + ":" + d.Msg
}

// toLSP converts an internal [lexer.Diagnostic] to the LSP wire shape.
// The Range degenerates to a zero-length cursor when End is the zero
// position so every diagnostic still gets a clickable location.
//
// Related links (collision "first declared here", import-cycle path,
// etc.) ride along via DiagnosticRelatedInformation so the editor's
// quick-peek surfaces the partner location with a clickable jump.
//
// srcByFile maps a diagnostic's source filename to that file's text so
// positions land on UTF-16 code-unit columns (the LSP unit) rather than
// the lexer's rune columns - they differ on any line carrying
// supplementary runes. A missing entry (empty string) degrades to a
// rune→unit copy via [utf16Position], which is correct for the BMP.
func toLSP(d lexer.Diagnostic, srcByFile map[string]string) protocol.Diagnostic {
	end := d.End
	if !end.IsValid() {
		end = d.Pos
	}
	out := protocol.Diagnostic{
		Range: protocol.Range{
			Start: utf16Position(srcByFile[d.Pos.Filename], d.Pos),
			End:   utf16Position(srcByFile[end.Filename], end),
		},
		Severity: lspSeverity(d.Severity),
		Code:     d.Code,
		Source:   "craftgo",
		Message:  d.Msg,
	}
	if len(d.Related) > 0 {
		related := make([]protocol.DiagnosticRelatedInformation, 0, len(d.Related))
		for _, r := range d.Related {
			rp := utf16Position(srcByFile[r.Pos.Filename], r.Pos)
			rng := protocol.Range{Start: rp, End: rp}
			related = append(related, protocol.DiagnosticRelatedInformation{
				Location: protocol.Location{
					URI:   protocol.DocumentURI(pathToFileURIString(r.Pos.Filename)),
					Range: rng,
				},
				Message: r.Msg,
			})
		}
		out.RelatedInformation = related
	}
	return out
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
