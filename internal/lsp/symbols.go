package lsp

import (
	"context"
	"encoding/json"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"

	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/lexer"
)

// onDocumentSymbol answers `textDocument/documentSymbol`. The result is
// a hierarchical outline (DocumentSymbol[]) — types/services nest their
// fields/methods so the editor's outline view shows structure.
func (s *Server) onDocumentSymbol(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
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

// documentSymbols walks the top-level declarations and emits one
// DocumentSymbol each. Type and service declarations carry nested
// children for their fields and methods respectively.
func documentSymbols(view snapshotView) []protocol.DocumentSymbol {
	if view.file == nil {
		return nil
	}
	out := make([]protocol.DocumentSymbol, 0, len(view.file.Decls))
	for _, d := range view.file.Decls {
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
			if f, ok := m.(*ast.Field); ok {
				children = append(children, fieldSymbol(f))
			}
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
		children := make([]protocol.DocumentSymbol, 0, len(v.Values))
		for _, ev := range v.Values {
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
			Kind:           protocol.SymbolKindTypeParameter,
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
	case *ast.ServiceDecl:
		children := make([]protocol.DocumentSymbol, 0, len(v.Methods))
		for _, m := range v.Methods {
			children = append(children, methodSymbol(m))
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

func methodSymbol(m *ast.Method) protocol.DocumentSymbol {
	r := rangeOfPosLen(m.Pos, len(m.Verb)+1+len(m.Name))
	detail := m.Verb + " " + m.Name
	return protocol.DocumentSymbol{
		Name:           m.Name,
		Detail:         detail,
		Kind:           protocol.SymbolKindMethod,
		Range:          r,
		SelectionRange: rangeOfPosLen(positionAfter(m.Pos, len(m.Verb)+1), len(m.Name)),
	}
}

// positionAfter is a tiny helper for advancing a column count; it keeps
// the symbol code free of inline arithmetic and makes the intent obvious.
func positionAfter(p lexer.Position, n int) lexer.Position {
	p.Column += n
	return p
}
