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

// onDefinition answers `textDocument/definition`. The cursor must sit on
// an identifier that names a top-level declaration; we walk the file's
// declarations and return the location of the matching name. Cross-file
// resolution is not implemented - callers receive an empty list and the
// editor falls back to its own workspace-symbol lookup.
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
	// Context-aware lookup: a name like `AuthRequired` can legally be
	// both a middleware AND a same-named error / type / scalar decl
	// (middleware lives in its own decl namespace; types/enums/errors/
	// scalars share another). Without context the linear `findDecl`
	// returns whichever appeared first in source order - which is wrong
	// for any of those overlap cases. We resolve by surrounding syntax:
	//   - inside `@middlewares(...)`   → prefer MiddlewareDecl
	//   - inside `@errors(...)`        → prefer ErrorDecl
	//   - in a type-shape position     → exclude MiddlewareDecl
	//
	// When a context is known we ONLY return decls of the matching kind
	// across all project files (in-file first, then cross-file). The
	// generic fallback runs only when the cursor has no resolvable
	// context - returning a wrong-kind decl is worse than returning
	// nothing.
	lookupCtx := refContextAt(view, idx, params.Position)
	qualified := qualifiedNameAt(view, idx)
	if lookupCtx != "" {
		if d := findDeclKindAware(view.file, tok.Text, lookupCtx); d != nil {
			return reply(ctx, []protocol.Location{{
				URI:   params.TextDocument.URI,
				Range: rangeOfPosLen(d.DeclPos(), len(d.DeclName())),
			}}, nil)
		}
		files, root := s.projectFilesWithRoot(uriToPath(string(params.TextDocument.URI)), src)
		imports := currentImports(view.file)
		if d, pf, ok := findDeclAcrossKindAware(files, qualified, imports, root, lookupCtx); ok {
			return reply(ctx, []protocol.Location{{
				URI:   uri.New(pathToFileURIString(pf.path)),
				Range: rangeOfPosLen(d.DeclPos(), len(d.DeclName())),
			}}, nil)
		}
		return reply(ctx, []protocol.Location{}, nil)
	}
	// In-file lookup first - fast path, avoids walking the project tree
	// when the user clicks on a same-file reference.
	if d := findDecl(view.file, tok.Text); d != nil {
		return reply(ctx, []protocol.Location{{
			URI:   params.TextDocument.URI,
			Range: rangeOfPosLen(d.DeclPos(), len(d.DeclName())),
		}}, nil)
	}
	// Cross-file lookup - qualified `pkg.Name` or bare name declared in
	// a sibling package. We rebuild the name from the surrounding
	// tokens so `users.UserRef` resolves whether the cursor was on the
	// `users` half or the `UserRef` half.
	files, root := s.projectFilesWithRoot(uriToPath(string(params.TextDocument.URI)), src)
	imports := currentImports(view.file)
	if d, pf, ok := findDeclAcross(files, qualified, imports, root); ok {
		return reply(ctx, []protocol.Location{{
			URI:   uri.New(pathToFileURIString(pf.path)),
			Range: rangeOfPosLen(d.DeclPos(), len(d.DeclName())),
		}}, nil)
	}
	return reply(ctx, []protocol.Location{}, nil)
}

// refContextAt classifies the cursor's surrounding syntax into a
// lookup-context string consumed by [findDeclKindAware]:
//
//   - "middlewares" / "errors": cursor sits inside that decorator's args
//   - "type":                   cursor sits in a type-shape position
//     (mixin, field type, request, response, generic arg, error category)
//   - "":                       could not classify - caller should fall
//     back to the generic [findDecl]
//
// The detection is purely token-based: we walk a small window of tokens
// around the cursor looking for shape markers (`@<ident>(`, `:` after
// the cursor, `<` opening generic args, `request`/`response`/`error`/
// `extends`/`type` keywords just before, ...). Token-level is enough
// here because we only need to disambiguate between kinds the parser
// already separated.
func refContextAt(view snapshotView, idx int, pos protocol.Position) string {
	if decName, ok := decoratorArgContext(view, pos); ok {
		switch decName {
		case "middlewares", "errors":
			return decName
		}
		return ""
	}
	if isTypeShapePosition(view, idx) {
		return "type"
	}
	return ""
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

// currentImports returns the imports slice of f, or nil when f has no
// imports section. Pulled out so the closing test cases stay readable.
func currentImports(f *ast.File) []*ast.Import {
	if f == nil {
		return nil
	}
	return f.Imports
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

// onReferences answers `textDocument/references`. We tokenise the source
// once and return every token whose text equals the symbol name under the
// cursor - a straight name match. False positives are possible (a string
// literal containing the same word would be skipped because string
// content lives inside a single token), but the heuristic is good enough
// for v0.1; a real resolver lands with the workspace-wide pass.
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
	out := nameMatches(view, params.TextDocument.URI, tok.Text, params.Context.IncludeDeclaration)
	return reply(ctx, out, nil)
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
