package lsp

import (
	"errors"
	"io/fs"
	"net/url"
	"path/filepath"
	"strconv"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// buildProjectDiagnostics analyses the project of u holding src and returns its
// deduplicated diagnostics by file path, u always included, and the design root.
func (s *server) buildProjectDiagnostics(u uri.URI, src string) (map[string][]protocol.Diagnostic, string) {
	fsPath := uriToPath(string(u))
	v := s.loadProject(fsPath, src)

	// Each file's text places its diagnostics on UTF-16 columns; an untagged
	// diagnostic belongs to the buffer.
	srcByFile := make(map[string]string, len(v.files)+1)
	for _, lf := range v.files {
		srcByFile[lf.path] = lf.src
	}
	srcByFile[""] = src

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
		perFile[key] = append(perFile[key], v.toLSP(d, srcByFile, u))
	}
	if _, ok := perFile[fsPath]; !ok && fsPath != "" {
		perFile[fsPath] = []protocol.Diagnostic{}
	}
	return perFile, v.root
}

// manifestPath returns the path of the manifest in the design folder root.
func manifestPath(root string) string {
	return filepath.Join(root, config.Filename)
}

// manifestDiagnostics loads the manifest at path and returns, at its start,
// the error that keeps it from loading, else a warning for each key it
// ignores; ok is false when path is gone.
func manifestDiagnostics(path string) (diags []protocol.Diagnostic, ok bool) {
	cfg, err := config.Load(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return []protocol.Diagnostic{}, false
	case err != nil:
		return []protocol.Diagnostic{{Severity: protocol.DiagnosticSeverityError, Source: "craftgo", Message: err.Error()}}, true
	}
	diags = []protocol.Diagnostic{}
	for _, w := range cfg.Warnings {
		diags = append(diags, protocol.Diagnostic{Severity: protocol.DiagnosticSeverityWarning, Source: "craftgo", Message: w})
	}
	return diags, true
}

// uriToPath returns the file path of a file:// URI, or "" for any other URI.
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
	// file:///C:/x parses to /C:/x; drop the slash before the drive letter.
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}

// keyOf identifies a diagnostic by file, position, code and message, so a
// repeat is published once.
func keyOf(d lexer.Diagnostic) string {
	return d.Pos.Filename + ":" + strconv.Itoa(d.Pos.Line) + ":" + strconv.Itoa(d.Pos.Column) + ":" + d.Code + ":" + d.Msg
}

// toLSP converts d to LSP, placing columns in UTF-16 units with the file texts
// in srcByFile; an invalid End collapses the range to Pos. A related location
// in the buffer takes its URI, current.
func (v projectView) toLSP(d lexer.Diagnostic, srcByFile map[string]string, current protocol.DocumentURI) protocol.Diagnostic {
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
					URI:   v.uriOf(r.Pos.Filename, current),
					Range: rng,
				},
				Message: r.Msg,
			})
		}
		out.RelatedInformation = related
	}
	return out
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
