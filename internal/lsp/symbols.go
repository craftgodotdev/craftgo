package lsp

import (
	"context"
	"strings"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// onDocumentSymbol answers `textDocument/documentSymbol` with the buffer's outline.
func (s *server) onDocumentSymbol(_ context.Context, params protocol.DocumentSymbolParams) (any, error) {
	r, ok := s.open(params.TextDocument.URI)
	if !ok {
		return []protocol.DocumentSymbol{}, nil
	}
	return documentSymbols(r.view()), nil
}

// onWorkspaceSymbol answers `workspace/symbol` with the project's declarations
// whose name contains the query, ignoring case.
func (s *server) onWorkspaceSymbol(_ context.Context, params protocol.WorkspaceSymbolParams) (any, error) {
	// The project is found from an open document.
	anchorPath, anchorSrc := s.anyOpenDocument()
	if anchorPath == "" {
		return []protocol.SymbolInformation{}, nil
	}
	query := strings.ToLower(params.Query)
	var out []protocol.SymbolInformation
	for _, p := range s.loadProject(anchorPath, anchorSrc).files {
		for _, d := range p.file.Decls {
			name := d.DeclName()
			if name == "" || !strings.Contains(strings.ToLower(name), query) {
				continue
			}
			out = append(out, protocol.SymbolInformation{
				Name: name,
				Kind: infoOf(d).symbol,
				Location: protocol.Location{
					URI:   uri.File(p.path),
					Range: spanRange(p.src, d.DeclPos(), len(name)),
				},
				ContainerName: containerNameFromFile(p.file),
			})
		}
	}
	return out, nil
}

// containerNameFromFile returns f's package name.
func containerNameFromFile(f *ast.File) string {
	if f == nil || f.Package == nil {
		return ""
	}
	return f.Package.Name
}

// anyOpenDocument returns the path and text of some open document, or empty
// strings when none is open.
func (s *server) anyOpenDocument() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for u, text := range s.docs {
		return uriToPath(string(u)), text
	}
	return "", ""
}

// documentSymbols returns one symbol per named declaration. An unnamed one is
// skipped: VS Code rejects the whole outline over an empty name.
func documentSymbols(view snapshotView) []protocol.DocumentSymbol {
	if view.file == nil {
		return nil
	}
	out := make([]protocol.DocumentSymbol, 0, len(view.file.Decls))
	for _, d := range view.file.Decls {
		if d.DeclName() == "" {
			continue
		}
		out = append(out, declSymbol(view.src, d))
	}
	return out
}

// declSymbol returns the outline entry of d, with a type's fields, an enum's
// values and a service's methods as children and an event's payload in its
// detail.
func declSymbol(src string, d ast.Decl) protocol.DocumentSymbol {
	info := infoOf(d)
	r := spanRange(src, d.DeclPos(), len(d.DeclName()))
	sym := protocol.DocumentSymbol{Name: d.DeclName(), Detail: info.summary, Kind: info.symbol, Range: r, SelectionRange: r}
	switch v := d.(type) {
	case *ast.TypeDecl:
		for _, m := range v.Body {
			if f, ok := m.(*ast.Field); ok && f.Name != "" {
				sym.Children = append(sym.Children, fieldSymbol(src, f))
			}
		}
	case *ast.EnumDecl:
		for _, ev := range v.EnumValues() {
			if ev.Name == "" {
				continue
			}
			er := spanRange(src, ev.Pos, len(ev.Name))
			sym.Children = append(sym.Children, protocol.DocumentSymbol{
				Name:           ev.Name,
				Kind:           protocol.SymbolKindEnumMember,
				Range:          er,
				SelectionRange: er,
			})
		}
	case *ast.EventDecl:
		sym.Range = spanRange(src, v.Pos, len("event")+1+len(v.Name))
		sym.SelectionRange = sym.Range
		if v.Payload != nil && v.Payload.Type != nil && v.Payload.Type.Name != nil {
			payload := v.Payload.Type.Name.String()
			if v.Payload.Array {
				payload += "[]"
			}
			sym.Detail += " (" + payload + ")"
		}
	case *ast.ServiceDecl:
		for _, member := range v.Members {
			if m, ok := member.(*ast.Method); ok && m.Name != "" {
				sym.Children = append(sym.Children, methodSymbol(src, m))
			}
		}
	}
	return sym
}

func fieldSymbol(src string, f *ast.Field) protocol.DocumentSymbol {
	r := spanRange(src, f.Pos, len(f.Name))
	return protocol.DocumentSymbol{
		Name:           f.Name,
		Kind:           protocol.SymbolKindField,
		Range:          r,
		SelectionRange: r,
	}
}

func methodSymbol(src string, m *ast.Method) protocol.DocumentSymbol {
	r := spanRange(src, m.Pos, len(m.Verb)+1+len(m.Name))
	// Detail: `verb Name (Req → Resp)`, without a missing side.
	detail := m.Verb + " " + m.Name
	req, resp := "", ""
	if m.Request != nil && m.Request.Name != nil {
		req = m.Request.Name.String()
	}
	if m.Response != nil && m.Response.Type != nil && m.Response.Type.Name != nil {
		resp = m.Response.Type.Name.String()
	}
	switch {
	case req != "" && resp != "":
		detail += " (" + req + " → " + resp + ")"
	case req != "":
		detail += " (" + req + ")"
	case resp != "":
		detail += " (→ " + resp + ")"
	}
	namePos := m.Pos
	namePos.Column += len(m.Verb) + 1
	namePos.Offset += len(m.Verb) + 1
	return protocol.DocumentSymbol{
		Name:           m.Name,
		Detail:         detail,
		Kind:           protocol.SymbolKindMethod,
		Range:          r,
		SelectionRange: spanRange(src, namePos, len(m.Name)),
	}
}
