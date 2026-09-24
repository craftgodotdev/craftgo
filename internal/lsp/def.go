package lsp

import (
	"context"

	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// onDefinition answers `textDocument/definition` with the declaration the
// identifier at the cursor names, among the kinds [lookupKindAt] allows.
func (s *server) onDefinition(_ context.Context, params protocol.DefinitionParams) (any, error) {
	r, ok := s.open(params.TextDocument.URI)
	if !ok {
		return []protocol.Location{}, nil
	}
	v := r.project()
	view := r.view()
	c := view.cursorAt(params.Position)
	if c.at < 0 || view.tokens[c.at].Kind != lexer.Ident {
		return []protocol.Location{}, nil
	}
	if loc, ok := r.enumValueDefinition(c); ok {
		return []protocol.Location{loc}, nil
	}
	d := v.lookup(qualifiedNameAt(view, c.at), lookupKindAt(view, c))
	if d == nil {
		return []protocol.Location{}, nil
	}
	return []protocol.Location{v.locationOf(d.DeclPos(), len(d.DeclName()), r.uri)}, nil
}

// enumValueDefinition resolves a value named in a field's `@default(...)` or
// `@example(...)` to its declaration in the field's enum type.
func (r *request) enumValueDefinition(c cursor) (protocol.Location, bool) {
	view := r.view()
	decName, _, ok := decoratorArgContext(view, c)
	if !ok || (decName != "default" && decName != "example") {
		return protocol.Location{}, false
	}
	f := fieldAtCursor(view, c)
	if f == nil || f.Type == nil || f.Type.Named == nil || f.Type.Named.Name == nil {
		return protocol.Location{}, false
	}
	v := r.project()
	e, ok := v.lookup(f.Type.Named.Name.String(), semantic.EnumDecls).(*ast.EnumDecl)
	if !ok {
		return protocol.Location{}, false
	}
	for _, val := range e.EnumValues() {
		if val.Name == view.tokens[c.at].Text {
			return v.locationOf(val.Pos, len(val.Name), r.uri), true
		}
	}
	return protocol.Location{}, false
}

// enclosingDecl returns the index of the keyword of the declaration holding
// token idx (-1 at file level) and the brace depth there. The walk is forward:
// a keyword is also a legal field name, and only its depth tells them apart.
func enclosingDecl(view snapshotView, idx int) (int, int) {
	last, depth := -1, 0
	for i := range view.outsideParens(-1) {
		if i >= idx {
			break
		}
		switch k := view.tokens[i].Kind; {
		case k == lexer.LBrace:
			depth++
		case k == lexer.RBrace:
			if depth > 0 {
				depth--
			}
			if depth == 0 {
				last = -1
			}
		case depth == 0 && isDeclKeyword(k):
			last = i
		}
	}
	return last, depth
}

// lookupKindAt returns the declaration kinds the identifier under the cursor
// may resolve to, from the tokens around it.
func lookupKindAt(view snapshotView, c cursor) semantic.DeclKind {
	if decName, _, ok := decoratorArgContext(view, c); ok {
		switch decName {
		case "middlewares":
			return semantic.MiddlewareDecls
		case "errors":
			return semantic.ErrorDecls
		}
		return semantic.AnyDecl
	}
	if isServiceHeaderPosition(view, c.at) {
		return semantic.ServiceDecls
	}
	if isTypeShapePosition(view, c.at) {
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
			kw, _ := enclosingDecl(view, idx)
			return view.kind(kw) != lexer.KwEvent
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
func (s *server) onReferences(_ context.Context, params protocol.ReferenceParams) (any, error) {
	r, ok := s.open(params.TextDocument.URI)
	if !ok {
		return []protocol.Location{}, nil
	}
	v := r.project()
	view := r.view()
	c := view.cursorAt(params.Position)
	if c.at < 0 || view.tokens[c.at].Kind != lexer.Ident {
		return []protocol.Location{}, nil
	}
	return v.nameMatches(view.tokens[c.at].Text, params.Context.IncludeDeclaration, r.uri), nil
}

// nameMatches returns the location of every identifier spelt name in the
// project; the first declaration of that name is left out unless includeDecl.
func (v projectView) nameMatches(name string, includeDecl bool, current protocol.DocumentURI) []protocol.Location {
	var declPos *lexer.Position
	for _, lf := range v.files {
		if d := findDecl(lf.file, name); d != nil {
			p := d.DeclPos()
			declPos = &p
			break
		}
	}
	var out []protocol.Location
	for _, lf := range v.files {
		u := v.uriOf(lf.path, current)
		for _, t := range lf.tokens {
			if t.Kind != lexer.Ident || t.Text != name || (!includeDecl && declPos != nil && t.Pos == *declPos) {
				continue
			}
			out = append(out, protocol.Location{URI: u, Range: rangeOf(lf.src, t)})
		}
	}
	return out
}

// onDocumentHighlight answers `textDocument/documentHighlight` with every
// identifier in the buffer spelt like the one at the cursor.
func (s *server) onDocumentHighlight(_ context.Context, params protocol.DocumentHighlightParams) (any, error) {
	r, ok := s.open(params.TextDocument.URI)
	if !ok {
		return []protocol.DocumentHighlight{}, nil
	}
	view := r.view()
	c := view.cursorAt(params.Position)
	if c.at < 0 || view.tokens[c.at].Kind != lexer.Ident {
		return []protocol.DocumentHighlight{}, nil
	}
	name := view.tokens[c.at].Text
	out := []protocol.DocumentHighlight{}
	for _, t := range view.tokens {
		if t.Kind != lexer.Ident || t.Text != name {
			continue
		}
		out = append(out, protocol.DocumentHighlight{Range: rangeOf(view.src, t), Kind: protocol.DocumentHighlightKindText})
	}
	return out, nil
}
