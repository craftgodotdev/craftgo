package lsp

import (
	"context"
	"encoding/json"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// onDefinition answers `textDocument/definition` with the declaration the
// identifier at the cursor names, among the kinds [lookupKindAt] allows.
func (s *server) onDefinition(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	var params protocol.DefinitionParams
	if err := json.Unmarshal(req.Params(), &params); err != nil {
		return reply(ctx, nil, err)
	}
	src := s.snapshot(params.TextDocument.URI)
	if src == "" {
		return reply(ctx, []protocol.Location{}, nil)
	}
	view := parseSnapshot(string(params.TextDocument.URI), src)
	idx, tok := view.tokenAt(params.Position.Line, params.Position.Character)
	if idx < 0 || tok.Kind != lexer.Ident {
		return reply(ctx, []protocol.Location{}, nil)
	}
	current := params.TextDocument.URI
	v := s.loadProject(uriToPath(string(current)), src)
	if loc, ok := enumValueDefinition(v, view, params.Position, tok.Text, current); ok {
		return reply(ctx, []protocol.Location{loc}, nil)
	}
	d := v.lookup(qualifiedNameAt(view, idx), lookupKindAt(view, idx, params.Position))
	if d == nil {
		return reply(ctx, []protocol.Location{}, nil)
	}
	return reply(ctx, []protocol.Location{v.locationOf(d.DeclPos(), len(d.DeclName()), current)}, nil)
}

// enumValueDefinition resolves a value named in a field's `@default(...)` or
// `@example(...)` to its declaration in the field's enum type.
func enumValueDefinition(v projectView, view snapshotView, pos protocol.Position, name string, current protocol.DocumentURI) (protocol.Location, bool) {
	decName, ok := decoratorArgContext(view, pos)
	if !ok || (decName != "default" && decName != "example") {
		return protocol.Location{}, false
	}
	f := fieldAtCursor(view, pos)
	if f == nil || f.Type == nil || f.Type.Named == nil || f.Type.Named.Name == nil {
		return protocol.Location{}, false
	}
	e, ok := v.lookup(f.Type.Named.Name.String(), semantic.EnumDecls).(*ast.EnumDecl)
	if !ok {
		return protocol.Location{}, false
	}
	for _, val := range e.EnumValues() {
		if val.Name == name {
			return v.locationOf(val.Pos, len(val.Name), current), true
		}
	}
	return protocol.Location{}, false
}

// enclosingDeclKeyword returns the keyword of the declaration holding token idx
// ([lexer.EOF] at file level) and the brace depth there. The walk is forward:
// a keyword is also a legal field name, and only its depth tells them apart.
func enclosingDeclKeyword(view snapshotView, idx int) (lexer.Kind, int) {
	last := lexer.EOF
	depth := 0
	for i := 0; i < idx && i < len(view.tokens); i++ {
		switch k := view.tokens[i].Kind; k {
		case lexer.LBrace:
			depth++
		case lexer.RBrace:
			if depth > 0 {
				depth--
			}
			if depth == 0 {
				last = lexer.EOF
			}
		case lexer.KwService, lexer.KwExtend, lexer.KwType, lexer.KwEnum,
			lexer.KwError, lexer.KwScalar, lexer.KwMiddleware, lexer.KwEvent:
			if depth == 0 {
				last = k
			}
		}
	}
	return last, depth
}

// lookupKindAt returns the declaration kinds a definition at token idx may
// resolve to, from the tokens around it.
func lookupKindAt(view snapshotView, idx int, pos protocol.Position) semantic.DeclKind {
	if decName, ok := decoratorArgContext(view, pos); ok {
		switch decName {
		case "middlewares":
			return semantic.MiddlewareDecls
		case "errors":
			return semantic.ErrorDecls
		}
		return semantic.AnyDecl
	}
	if isServiceHeaderPosition(view, idx) {
		return semantic.ServiceDecls
	}
	if isTypeShapePosition(view, idx) {
		return semantic.TypeShapeDecls
	}
	return semantic.AnyDecl
}

// isServiceHeaderPosition reports whether token idx is the name in a
// `service X` or `extend service X` header.
func isServiceHeaderPosition(view snapshotView, idx int) bool {
	if idx <= 0 || idx >= len(view.tokens) {
		return false
	}
	return view.tokens[idx-1].Kind == lexer.KwService
}

// isTypeShapePosition reports whether token idx names a type (a field type, a
// mixin, a clause, a generic argument), judged by the tokens before it.
func isTypeShapePosition(view snapshotView, idx int) bool {
	if idx < 0 || idx >= len(view.tokens) {
		return false
	}
	for i := idx - 1; i >= 0; i-- {
		t := view.tokens[i]
		switch t.Kind {
		case lexer.Colon, lexer.LAngle, lexer.LBracket, lexer.RBracket, lexer.Comma:
			return true
		case lexer.KwRequest, lexer.KwResponse, lexer.KwPayload, lexer.KwError, lexer.KwType, lexer.KwScalar, lexer.KwEnum:
			return true
		case lexer.KwEvent:
			// `event X` declares a contract; inside a type body `event` is a field.
			kw, _ := enclosingDeclKeyword(view, idx)
			return kw != lexer.KwEvent
		case lexer.KwService, lexer.KwExtend:
			// A service name, not a type.
			return false
		case lexer.Ident:
			// `name Type`: the cursor is on the field's type.
			return true
		case lexer.At, lexer.LParen, lexer.RParen:
			return false
		}
	}
	return false
}

// qualifiedNameAt returns the identifier at idx, or `pkg.Name` when it is
// either half of a dotted reference.
func qualifiedNameAt(view snapshotView, idx int) string {
	tok := view.tokens[idx]
	if tok.Kind != lexer.Ident {
		return tok.Text
	}
	if idx >= 2 && view.tokens[idx-1].Kind == lexer.Dot && view.tokens[idx-2].Kind == lexer.Ident {
		return view.tokens[idx-2].Text + "." + tok.Text
	}
	if idx+2 < len(view.tokens) && view.tokens[idx+1].Kind == lexer.Dot && view.tokens[idx+2].Kind == lexer.Ident {
		return tok.Text + "." + view.tokens[idx+2].Text
	}
	return tok.Text
}

// onReferences answers `textDocument/references` with every identifier in the
// project spelt like the one at the cursor.
func (s *server) onReferences(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	var params protocol.ReferenceParams
	if err := json.Unmarshal(req.Params(), &params); err != nil {
		return reply(ctx, nil, err)
	}
	src := s.snapshot(params.TextDocument.URI)
	if src == "" {
		return reply(ctx, []protocol.Location{}, nil)
	}
	view := parseSnapshot(string(params.TextDocument.URI), src)
	idx, tok := view.tokenAt(params.Position.Line, params.Position.Character)
	if idx < 0 || tok.Kind != lexer.Ident {
		return reply(ctx, []protocol.Location{}, nil)
	}
	out := s.projectNameMatches(view, params.TextDocument.URI, src, tok.Text, params.Context.IncludeDeclaration)
	return reply(ctx, out, nil)
}

// projectNameMatches returns the location of every identifier spelt name in
// the buffer's project, or in the buffer alone outside a project.
func (s *server) projectNameMatches(view snapshotView, currentURI protocol.DocumentURI, currentSrc, name string, includeDecl bool) []protocol.Location {
	v := s.loadProject(uriToPath(string(currentURI)), currentSrc)
	if v.root == "" {
		return nameMatches(view, currentURI, name, includeDecl)
	}
	var declPos *lexer.Position
	var declURI protocol.DocumentURI
	for _, p := range v.files {
		if d := findDecl(p.file, name); d != nil {
			pos := d.DeclPos()
			declPos = &pos
			declURI = protocol.DocumentURI(pathToFileURIString(p.path))
			break
		}
	}
	var out []protocol.Location
	for _, p := range v.files {
		fileURI := protocol.DocumentURI(pathToFileURIString(p.path))
		for _, t := range p.tokens {
			if t.Kind != lexer.Ident || t.Text != name {
				continue
			}
			if !includeDecl && declPos != nil && fileURI == declURI && t.Pos == *declPos {
				continue
			}
			out = append(out, protocol.Location{URI: fileURI, Range: rangeOf(t)})
		}
	}
	return out
}

// nameMatches returns the location of every identifier in view spelt name.
func nameMatches(view snapshotView, u protocol.DocumentURI, name string, includeDecl bool) []protocol.Location {
	var declPos *lexer.Position
	if d := findDecl(view.file, name); d != nil {
		p := d.DeclPos()
		declPos = &p
	}
	var out []protocol.Location
	for _, t := range view.tokens {
		if t.Kind != lexer.Ident || t.Text != name {
			continue
		}
		if !includeDecl && declPos != nil && t.Pos == *declPos {
			continue
		}
		out = append(out, protocol.Location{URI: u, Range: rangeOf(t)})
	}
	return out
}

// onDocumentHighlight answers `textDocument/documentHighlight` with every
// identifier in the buffer spelt like the one at the cursor.
func (s *server) onDocumentHighlight(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	var params protocol.DocumentHighlightParams
	if err := json.Unmarshal(req.Params(), &params); err != nil {
		return reply(ctx, nil, err)
	}
	src := s.snapshot(params.TextDocument.URI)
	if src == "" {
		return reply(ctx, []protocol.DocumentHighlight{}, nil)
	}
	view := parseSnapshot(string(params.TextDocument.URI), src)
	idx, tok := view.tokenAt(params.Position.Line, params.Position.Character)
	if idx < 0 || tok.Kind != lexer.Ident {
		return reply(ctx, []protocol.DocumentHighlight{}, nil)
	}
	out := []protocol.DocumentHighlight{}
	for _, t := range view.tokens {
		if t.Kind != lexer.Ident || t.Text != tok.Text {
			continue
		}
		kind := protocol.DocumentHighlightKindText
		out = append(out, protocol.DocumentHighlight{Range: rangeOf(t), Kind: kind})
	}
	return reply(ctx, out, nil)
}
