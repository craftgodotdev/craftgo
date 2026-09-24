package lsp

import (
	"context"
	"encoding/json"
	"strings"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// onDocumentSymbol answers `textDocument/documentSymbol` with the buffer's outline.
func (s *server) onDocumentSymbol(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	var params protocol.DocumentSymbolParams
	if err := json.Unmarshal(req.Params(), &params); err != nil {
		return reply(ctx, nil, err)
	}
	src := s.snapshot(params.TextDocument.URI)
	if src == "" {
		return reply(ctx, []interface{}{}, nil)
	}
	view := parseSnapshot(string(params.TextDocument.URI), src)
	syms := documentSymbols(view)
	out := make([]interface{}, 0, len(syms))
	for _, s := range syms {
		out = append(out, s)
	}
	return reply(ctx, out, nil)
}

// onWorkspaceSymbol answers `workspace/symbol` with the project's declarations
// whose name contains the query, ignoring case.
func (s *server) onWorkspaceSymbol(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	var params protocol.WorkspaceSymbolParams
	if err := json.Unmarshal(req.Params(), &params); err != nil {
		return reply(ctx, nil, err)
	}
	// The project is found from an open document.
	anchorPath, anchorSrc := s.anyOpenDocument()
	if anchorPath == "" {
		return reply(ctx, []protocol.SymbolInformation{}, nil)
	}
	query := strings.ToLower(params.Query)
	var out []protocol.SymbolInformation
	for _, p := range s.loadProject(anchorPath, anchorSrc).files {
		fileURI := uri.File(p.path)
		for _, d := range p.file.Decls {
			name := d.DeclName()
			if name == "" || !strings.Contains(strings.ToLower(name), query) {
				continue
			}
			out = append(out, protocol.SymbolInformation{
				Name: name,
				Kind: workspaceSymbolKind(d),
				Location: protocol.Location{
					URI:   protocol.DocumentURI(fileURI),
					Range: rangeOfPosLen(d.DeclPos(), len(name)),
				},
				ContainerName: containerNameFromFile(p.file),
			})
		}
	}
	return reply(ctx, out, nil)
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
		out = append(out, declSymbol(d))
	}
	return out
}

func declSymbol(d ast.Decl) protocol.DocumentSymbol {
	pos := d.DeclPos()
	r := rangeOfPosLen(pos, len(d.DeclName()))
	switch v := d.(type) {
	case *ast.TypeDecl:
		children := make([]protocol.DocumentSymbol, 0, len(v.Body))
		for _, m := range v.Body {
			f, ok := m.(*ast.Field)
			if !ok || f.Name == "" {
				continue
			}
			children = append(children, fieldSymbol(f))
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
			er := rangeOfPosLen(ev.Pos, len(ev.Name))
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
		return eventSymbol(v)
	case *ast.ServiceDecl:
		children := make([]protocol.DocumentSymbol, 0, len(v.Members))
		for _, member := range v.Members {
			if m, ok := member.(*ast.Method); ok && m.Name != "" {
				children = append(children, methodSymbol(m))
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

func fieldSymbol(f *ast.Field) protocol.DocumentSymbol {
	r := rangeOfPosLen(f.Pos, len(f.Name))
	return protocol.DocumentSymbol{
		Name:           f.Name,
		Kind:           protocol.SymbolKindField,
		Range:          r,
		SelectionRange: r,
	}
}

// eventSymbol returns the outline entry `event Name (Payload)`.
func eventSymbol(e *ast.EventDecl) protocol.DocumentSymbol {
	r := rangeOfPosLen(e.Pos, len("event")+1+len(e.Name))
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

func methodSymbol(m *ast.Method) protocol.DocumentSymbol {
	r := rangeOfPosLen(m.Pos, len(m.Verb)+1+len(m.Name))
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
	return protocol.DocumentSymbol{
		Name:           m.Name,
		Detail:         detail,
		Kind:           protocol.SymbolKindMethod,
		Range:          r,
		SelectionRange: rangeOfPosLen(namePos, len(m.Name)),
	}
}
