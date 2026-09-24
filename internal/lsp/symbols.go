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
				Kind: workspaceSymbolKind(d),
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

// workspaceSymbolKind returns the symbol kind of a declaration.
func workspaceSymbolKind(d ast.Decl) protocol.SymbolKind {
	switch d.(type) {
	case *ast.TypeDecl:
		return protocol.SymbolKindStruct
	case *ast.EnumDecl:
		return protocol.SymbolKindEnum
	case *ast.ErrorDecl:
		return protocol.SymbolKindClass
	case *ast.ScalarDecl:
		return protocol.SymbolKindClass
	case *ast.ServiceDecl:
		return protocol.SymbolKindInterface
	case *ast.MiddlewareDecl:
		return protocol.SymbolKindFunction
	case *ast.EventDecl:
		return protocol.SymbolKindEvent
	}
	return protocol.SymbolKindNull
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

func declSymbol(src string, d ast.Decl) protocol.DocumentSymbol {
	pos := d.DeclPos()
	r := spanRange(src, pos, len(d.DeclName()))
	switch v := d.(type) {
	case *ast.TypeDecl:
		children := make([]protocol.DocumentSymbol, 0, len(v.Body))
		for _, m := range v.Body {
			f, ok := m.(*ast.Field)
			if !ok || f.Name == "" {
				continue
			}
			children = append(children, fieldSymbol(src, f))
		}
		return protocol.DocumentSymbol{
			Name:           v.Name,
			Detail:         declSummary(d),
			Kind:           protocol.SymbolKindStruct,
			Range:          r,
			SelectionRange: r,
			Children:       children,
		}
	case *ast.EnumDecl:
		enumVals := v.EnumValues()
		children := make([]protocol.DocumentSymbol, 0, len(enumVals))
		for _, ev := range enumVals {
			if ev.Name == "" {
				continue
			}
			er := spanRange(src, ev.Pos, len(ev.Name))
			children = append(children, protocol.DocumentSymbol{
				Name:           ev.Name,
				Kind:           protocol.SymbolKindEnumMember,
				Range:          er,
				SelectionRange: er,
			})
		}
		return protocol.DocumentSymbol{
			Name:           v.Name,
			Detail:         declSummary(d),
			Kind:           protocol.SymbolKindEnum,
			Range:          r,
			SelectionRange: r,
			Children:       children,
		}
	case *ast.ErrorDecl:
		return protocol.DocumentSymbol{
			Name:           v.Name,
			Detail:         declSummary(d),
			Kind:           protocol.SymbolKindObject,
			Range:          r,
			SelectionRange: r,
		}
	case *ast.ScalarDecl:
		return protocol.DocumentSymbol{
			Name:           v.Name,
			Detail:         declSummary(d),
			Kind:           protocol.SymbolKindClass,
			Range:          r,
			SelectionRange: r,
		}
	case *ast.MiddlewareDecl:
		return protocol.DocumentSymbol{
			Name:           v.Name,
			Detail:         declSummary(d),
			Kind:           protocol.SymbolKindFunction,
			Range:          r,
			SelectionRange: r,
		}
	case *ast.EventDecl:
		return eventSymbol(src, v)
	case *ast.ServiceDecl:
		children := make([]protocol.DocumentSymbol, 0, len(v.Members))
		for _, member := range v.Members {
			if m, ok := member.(*ast.Method); ok && m.Name != "" {
				children = append(children, methodSymbol(src, m))
			}
		}
		return protocol.DocumentSymbol{
			Name:           v.Name,
			Detail:         declSummary(d),
			Kind:           protocol.SymbolKindInterface,
			Range:          r,
			SelectionRange: r,
			Children:       children,
		}
	}
	return protocol.DocumentSymbol{
		Name:           d.DeclName(),
		Kind:           protocol.SymbolKindClass,
		Range:          r,
		SelectionRange: r,
	}
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

// eventSymbol returns the outline entry `event Name (Payload)`.
func eventSymbol(src string, e *ast.EventDecl) protocol.DocumentSymbol {
	r := spanRange(src, e.Pos, len("event")+1+len(e.Name))
	detail := "event " + e.Name
	if e.Payload != nil && e.Payload.Type != nil && e.Payload.Type.Name != nil {
		payload := e.Payload.Type.Name.String()
		if e.Payload.Array {
			payload += "[]"
		}
		detail += " (" + payload + ")"
	}
	return protocol.DocumentSymbol{
		Name:           e.Name,
		Detail:         detail,
		Kind:           protocol.SymbolKindEvent,
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
