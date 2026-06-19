package lsp

import (
	"context"
	"encoding/json"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// onDocumentSymbol answers `textDocument/documentSymbol`. The result is
// a hierarchical outline (DocumentSymbol[]) - types/services nest their
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

// onWorkspaceSymbol answers `workspace/symbol`. Walks every parsed
// `.craftgo` file under the design root collecting top-level decls
// whose names match the query as a (case-insensitive) substring so
// the editor's Ctrl-T / Cmd-T picker surfaces project-wide symbols
// in one search. Empty query returns every symbol - that matches the
// LSP convention used by gopls / rust-analyzer where an empty query
// is "show me everything".
func (s *Server) onWorkspaceSymbol(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	var params protocol.WorkspaceSymbolParams
	if err := json.Unmarshal(req.Params(), &params); err != nil {
		return reply(ctx, nil, err)
	}
	// projectFilesWithRoot needs SOMETHING to anchor the design-root
	// search. Use the first open document's path; if no document is
	// open the workspace search is impossible (no design folder to
	// walk) - return empty rather than scanning the whole disk.
	anchorPath := s.anyOpenDocumentPath()
	if anchorPath == "" {
		return reply(ctx, []protocol.SymbolInformation{}, nil)
	}
	files, _ := s.projectFilesWithRoot(anchorPath, "")
	queryLower := lowerASCII(params.Query)
	var out []protocol.SymbolInformation
	for _, p := range files {
		if p.file == nil {
			continue
		}
		fileURI := uri.New(pathToFileURIString(p.path))
		for _, d := range p.file.Decls {
			name := d.DeclName()
			if name == "" {
				continue
			}
			if queryLower != "" && !containsLower(name, queryLower) {
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

// workspaceSymbolKind picks the LSP SymbolKind for a top-level decl.
// Mirrors [declSymbol]'s mapping so the workspace picker and the
// per-file outline use the same icons.
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
	}
	return protocol.SymbolKindNull
}

// containerNameFromFile returns the package name so the workspace
// picker groups symbols by package in its sub-label ("Pkg • Name").
func containerNameFromFile(f *ast.File) string {
	if f == nil || f.Package == nil {
		return ""
	}
	return f.Package.Name
}

// anyOpenDocumentPath returns the filesystem path of any currently
// open document. The lookup is order-independent; we just need
// somewhere to start the design-root walk. Returns empty string when
// no documents are open.
func (s *Server) anyOpenDocumentPath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for u := range s.docs {
		return uriToPath(string(u))
	}
	return ""
}

// lowerASCII / containsLower are tiny case-insensitive helpers so the
// workspace-symbol filter stays allocation-free on the hot path.
func lowerASCII(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

func containsLower(haystack, needleLower string) bool {
	if needleLower == "" {
		return true
	}
	hLower := lowerASCII(haystack)
	for i := 0; i+len(needleLower) <= len(hLower); i++ {
		if hLower[i:i+len(needleLower)] == needleLower {
			return true
		}
	}
	return false
}

// documentSymbols walks the top-level declarations and emits one
// DocumentSymbol each. Type and service declarations carry nested
// children for their fields and methods respectively.
//
// Declarations whose name token has not been parsed yet (typical
// while the author is mid-typing - e.g. just `service` with no
// identifier yet) are filtered out entirely: VS Code's
// `DocumentSymbol` validator rejects a falsy `Name` with
// "name must not be falsy", which crashes the symbol provider for
// the whole file. Skipping incomplete decls keeps the outline view
// usable while typing.
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
	case *ast.ServiceDecl:
		children := make([]protocol.DocumentSymbol, 0, len(v.Methods()))
		for _, m := range v.Methods() {
			if m.Name == "" {
				continue
			}
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
	// Build a one-line signature: "verb name (Req -> Resp)" so the
	// outline preview tells the user what the method binds + returns
	// without expanding. Missing slots collapse gracefully -
	// `request` only shows the request type, `response` only shows
	// the response, no body shows neither.
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
