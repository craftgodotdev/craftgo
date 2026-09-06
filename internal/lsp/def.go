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

// onDefinition answers `textDocument/definition`. The cursor must sit on
// an identifier naming a declaration; the name resolves through the
// semantic project (a qualified `pkg.Name` in that package, a bare name
// in the buffer's package first and then in any sibling package), and
// the surrounding syntax narrows the kinds considered so a click inside
// `@middlewares(X)` never lands on a same-named type. A cursor that
// names nothing returns an empty list.
func (s *Server) onDefinition(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
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

// enumValueDefinition resolves a cursor sitting on an enum-value name inside
// `@default(...)` / `@example(...)` to that value's declaration. The field's
// declared type names the enum; the matching value's position inside that
// enum's body is the target. Returns false when the cursor is not in such a
// position or the name is not a value of the field's enum type.
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

// lookupKindAt classifies the cursor's surrounding syntax into the
// declaration kinds a definition lookup may return:
//
//   - inside `@middlewares(...)` / `@errors(...)`: that decorator's kind
//   - the name in a `service X` / `extend service X` header: the primary
//     service, so a click on an extend's name lands on the block it
//     continues
//   - a type-shape position (mixin, field type, request, response,
//     generic arg, error category): every kind but middleware
//   - otherwise: every kind
//
// The detection is purely token-based: a small window of tokens around
// the cursor is inspected for shape markers (`@<ident>(`, `:`, `<`,
// `request` / `response` / `error` / `type` keywords, ...), which is
// enough to tell apart the kinds the parser already separated.
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

// isServiceHeaderPosition reports whether view.tokens[idx] is the name in
// a `service X` / `extend service X` header - i.e. the token right after
// the `service` keyword. Comments are captured off-stream by the lexer,
// so the preceding token is the previous meaningful one.
func isServiceHeaderPosition(view snapshotView, idx int) bool {
	if idx <= 0 || idx >= len(view.tokens) {
		return false
	}
	return view.tokens[idx-1].Kind == lexer.KwService
}

// isTypeShapePosition reports whether view.tokens[idx] is being used as
// a type-shape reference - i.e. anywhere a TypeDecl / EnumDecl /
// ScalarDecl / ErrorDecl can appear by spec. The check is conservative:
// false negatives fall through to the generic findDecl, which is the
// existing behaviour.
func isTypeShapePosition(view snapshotView, idx int) bool {
	if idx < 0 || idx >= len(view.tokens) {
		return false
	}
	// Walk back through whitespace-equivalent tokens to find the previous
	// meaningful token. Helpful preceding tokens that mark a type-shape
	// position:
	//   - `:` (field type after `name:`)
	//   - `request` / `response` keywords
	//   - `<` (generic arg list, possibly nested)
	//   - `[` / `]` (array element type wrapper)
	//   - the start of a type body where a bare ident is parsed as a
	//     mixin reference (preceded by `{` or `\n` inside a type body -
	//     hard to detect token-only without AST help)
	for i := idx - 1; i >= 0; i-- {
		t := view.tokens[i]
		switch t.Kind {
		case lexer.Colon, lexer.LAngle, lexer.LBracket, lexer.RBracket, lexer.Comma:
			return true
		case lexer.KwRequest, lexer.KwResponse, lexer.KwError, lexer.KwType, lexer.KwScalar, lexer.KwEnum:
			return true
		case lexer.KwService, lexer.KwExtend:
			// A service name, not a type reference. Without this the walk
			// runs past the header into the previous declaration and the
			// first Ident it meets there classifies the cursor as "type".
			return false
		case lexer.Ident:
			// `<fieldName> <Type>` is the field syntax (no colon needed
			// in craftgo). An ident immediately before our cursor's
			// ident classifies the cursor as the type half of that
			// pair. Over-classifying here is safe: type-context only
			// excludes MiddlewareDecl, and middleware never appears
			// after a bare ident in any valid construct.
			return true
		case lexer.At, lexer.LParen, lexer.RParen:
			return false
		}
	}
	return false
}

// qualifiedNameAt returns either the bare identifier at idx or the
// `pkg.Name` form when the surrounding tokens make the cursor sit on a
// dotted reference. The function inspects up to two tokens on either
// side so a click anywhere within `users . UserRef` recovers the same
// fully qualified string.
func qualifiedNameAt(view snapshotView, idx int) string {
	tok := view.tokens[idx]
	if tok.Kind != lexer.Ident {
		return tok.Text
	}
	// Cursor on the right half of `pkg.Name`.
	if idx >= 2 && view.tokens[idx-1].Kind == lexer.Dot && view.tokens[idx-2].Kind == lexer.Ident {
		return view.tokens[idx-2].Text + "." + tok.Text
	}
	// Cursor on the left half of `pkg.Name`.
	if idx+2 < len(view.tokens) && view.tokens[idx+1].Kind == lexer.Dot && view.tokens[idx+2].Kind == lexer.Ident {
		return tok.Text + "." + view.tokens[idx+2].Text
	}
	return tok.Text
}

// onReferences answers `textDocument/references`. The walker visits
// every `.craftgo` file under the design root so a reference search is
// project-wide, not buffer-only.
//
// Detection is purely token-based: every Ident token whose text
// matches the symbol's name counts. String literals (decorator args
// like `@pattern("X")`) are not scanned because their content lives
// inside a single String token, so literal text never collides with
// identifier references.
func (s *Server) onReferences(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
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

// projectNameMatches collects the position of every Ident token whose
// text equals name across the buffer's project. Outside a project root
// the current buffer alone is scanned.
func (s *Server) projectNameMatches(view snapshotView, currentURI protocol.DocumentURI, currentSrc, name string, includeDecl bool) []protocol.Location {
	v := s.loadProject(uriToPath(string(currentURI)), currentSrc)
	if v.root == "" {
		return nameMatches(view, currentURI, name, includeDecl)
	}
	// declPos pins the symbol's defining token across whichever file
	// owns the decl, so includeDecl=false can filter it out even when
	// the cursor lives in a different file from the declaration.
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

// nameMatches walks tokens for every Ident whose text equals name and
// returns the corresponding LSP locations. When includeDecl is false the
// declaration's defining token is filtered out so the editor can render
// "find usages" without the declaration site cluttering the list.
func nameMatches(view snapshotView, u protocol.DocumentURI, name string, includeDecl bool) []protocol.Location {
	var out []protocol.Location
	declPos := declSitePos(view.file, name)
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

func declSitePos(f *ast.File, name string) *lexer.Position {
	d := findDecl(f, name)
	if d == nil {
		return nil
	}
	p := d.DeclPos()
	return &p
}

// onDocumentHighlight answers `textDocument/documentHighlight`. Returns
// every occurrence of the symbol under the cursor IN THE CURRENT
// FILE so the editor can visually highlight all uses. Faster than
// `textDocument/references` because there is no project walk - the
// LSP client invokes this on cursor move, so cheapness matters more
// than completeness (cross-file lookup ships through `references`).
//
// Each highlight gets `Kind: Text` - the LSP spec also allows Read /
// Write kinds, but the DSL has no notion of "writing" an identifier
// (decls are immutable from the type checker's view), so the
// simpler Text kind matches actual semantics.
func (s *Server) onDocumentHighlight(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
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
