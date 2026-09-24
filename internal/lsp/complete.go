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

// onCompletion answers `textDocument/completion`; a document that is not open
// gets an empty list.
func (s *server) onCompletion(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	var params protocol.CompletionParams
	if err := json.Unmarshal(req.Params(), &params); err != nil {
		return reply(ctx, nil, err)
	}
	src := s.snapshot(params.TextDocument.URI)
	if src == "" {
		return reply(ctx, &protocol.CompletionList{}, nil)
	}
	view := parseSnapshot(string(params.TextDocument.URI), src)
	items := s.completionsAt(view, view.cursorAt(params.Position), string(params.TextDocument.URI), src)
	return reply(ctx, &protocol.CompletionList{IsIncomplete: false, Items: items}, nil)
}

// namedSlotCompletions answers the slots holding a name the project, the registry
// or the design folder knows; the bool reports such a slot, even with no answer.
func (s *server) namedSlotCompletions(view snapshotView, c cursor, currentURI, currentSrc string) ([]protocol.CompletionItem, bool) {
	prev, mid := view.token(c.prev), view.token(c.at)
	if prefix, ok := importPathPrefix(view, c); ok {
		return importPathCompletions(currentURI, prefix), true
	}
	// `import |`: the paths come with their quotes.
	if prev != nil && prev.Kind == lexer.KwImport {
		return quotedImportPathCompletions(currentURI), true
	}
	if isExtendServiceContext(view, c) {
		return s.serviceNameCompletions(currentURI, currentSrc), true
	}
	// `@name(|)`: the argument candidates of that decorator.
	if name, _, ok := decoratorArgContext(view, c); ok {
		if items := s.decoratorArgItems(view, c, currentURI, currentSrc, name); items != nil {
			return items, true
		}
		// A registered decorator with no closed set takes a free literal, so
		// nothing is offered; an unregistered name (a stray `(`) falls through.
		if _, known := semantic.Registry[name]; known {
			return nil, true
		}
	}
	// `pkg.|`: the dot is mid right after it is typed, prev once the member is.
	if mid != nil && mid.Kind == lexer.Dot {
		if pkg, ok := identBefore(view, c.at); ok {
			return s.packageDeclCompletions(currentURI, currentSrc, pkg), true
		}
	}
	if prev != nil && prev.Kind == lexer.Dot {
		if pkg, ok := identBefore(view, c.prev); ok {
			return s.packageDeclCompletions(currentURI, currentSrc, pkg), true
		}
	}
	// `@|` or `@na|`: decorator names.
	if mid != nil && mid.Kind == lexer.At {
		return decoratorCompletions(view, c, ""), true
	}
	if mid != nil && mid.Kind == lexer.Ident && prev != nil && prev.Kind == lexer.At {
		return decoratorCompletions(view, c, mid.Text), true
	}
	// `error |`: the error categories.
	if prev != nil && prev.Kind == lexer.KwError && (mid == nil || mid.Kind == lexer.Ident) {
		return errorCategoryCompletions(), true
	}
	// `package |`
	if prev != nil && prev.Kind == lexer.KwPackage && (mid == nil || mid.Kind == lexer.Ident) {
		return s.packageNameCompletions(currentURI, currentSrc), true
	}
	// `/{|}`: claimed here so the parameter's brace is not read as an opened block.
	if i, ok := pathParamContext(view, c); ok {
		return s.pathParamCompletions(view, currentURI, currentSrc, i), true
	}
	return nil, false
}

// completionsAt returns the candidate items for a cursor in view.
func (s *server) completionsAt(view snapshotView, c cursor, currentURI, currentSrc string) []protocol.CompletionItem {
	if items, ok := s.namedSlotCompletions(view, c, currentURI, currentSrc); ok {
		return items
	}
	prev, mid := view.token(c.prev), view.token(c.at)
	// Right after `{` with nothing typed (mid may be the closing `}`).
	if prev != nil && prev.Kind == lexer.LBrace && (mid == nil || mid.Kind != lexer.Ident) {
		block := blockAt(view, c)
		if !blockOffersKeysWhenOpened(block) {
			return nil
		}
		return s.blockKeyCompletions(block, currentURI, currentSrc)
	}
	// Past a `?` or `]` suffix the type is finished; the popup waits for `@`.
	if prev != nil && (prev.Kind == lexer.Question || prev.Kind == lexer.RBracket) && (mid == nil || mid.Kind != lexer.Ident) {
		return nil
	}
	// `type Page<|>` declares a parameter, whose name is the author's.
	if isTypeParamDeclPosition(view, c) {
		return nil
	}
	// `request |`, `response |` and `payload |` name a message type.
	if prev != nil && (prev.Kind == lexer.KwRequest || prev.Kind == lexer.KwResponse || prev.Kind == lexer.KwPayload) {
		return s.clauseTypeCompletions(currentURI, currentSrc)
	}
	if isTypeArgPosition(prev, mid) {
		return s.typeCompletionsProjectWide(currentURI, currentSrc)
	}
	if isScalarPrimitivePosition(view, c) {
		return scalarPrimitiveCompletions()
	}
	if isFieldTypePosition(view, c) {
		return s.typeCompletionsProjectWide(currentURI, currentSrc)
	}
	return s.blockKeyCompletions(blockAt(view, c), currentURI, currentSrc)
}

// completionBlock is the kind of block a cursor sits in.
type completionBlock uint8

const (
	blockFile completionBlock = iota
	// blockType is a `type` or `error` body.
	blockType
	blockEnum
	// blockService is a `service` or `extend service` body.
	blockService
	blockMethod
	blockEvent
)

// blockAt classifies the cursor's block by the enclosing declaration keyword
// and the brace depth; depth 2 inside a service is a method body.
func blockAt(view snapshotView, c cursor) completionBlock {
	kw, depth := enclosingDeclKeyword(view, c.lead())
	if depth == 0 {
		return blockFile
	}
	switch kw {
	case lexer.KwType, lexer.KwError:
		return blockType
	case lexer.KwEnum:
		return blockEnum
	case lexer.KwEvent:
		return blockEvent
	case lexer.KwService, lexer.KwExtend:
		if depth > 1 {
			return blockMethod
		}
		return blockService
	}
	return blockFile
}

// The keywords each block accepts as the first word of a member.
var (
	fileKeywords    = []string{"package", "import", "type", "enum", "error", "scalar", "service", "extend", "middleware", "event"}
	serviceKeywords = []string{"get", "post", "put", "patch", "delete", "head", "options"}
	methodKeywords  = []string{"request", "response"}
	eventKeywords   = []string{"payload"}
)

// blockKeyCompletions offers what block accepts: the declared types a mixin
// can name in a type body, nothing in an enum, keywords elsewhere.
func (s *server) blockKeyCompletions(block completionBlock, currentURI, currentSrc string) []protocol.CompletionItem {
	switch block {
	case blockType:
		return s.declCompletions(currentURI, currentSrc, semantic.TypeDecls)
	case blockEnum:
		return nil
	case blockService:
		return keywordCompletions(serviceKeywords...)
	case blockMethod:
		return keywordCompletions(methodKeywords...)
	case blockEvent:
		return keywordCompletions(eventKeywords...)
	}
	return keywordCompletions(fileKeywords...)
}

// blockOffersKeysWhenOpened reports whether a just-opened b lists its keys:
// only service, method and event bodies open on a short closed set.
func blockOffersKeysWhenOpened(b completionBlock) bool {
	switch b {
	case blockService, blockMethod, blockEvent:
		return true
	}
	return false
}

// pathParamContext reports whether the cursor is inside a route parameter
// (`/{|}` or `/{i|d}`) and returns the index of its `{`.
func pathParamContext(view snapshotView, c cursor) (int, bool) {
	i := c.prev
	if i < 0 {
		return 0, false
	}
	if view.tokens[i].Kind != lexer.LBrace {
		i--
	}
	if i < 1 || view.tokens[i].Kind != lexer.LBrace || view.tokens[i-1].Kind != lexer.Slash {
		return 0, false
	}
	if (i+1 < len(view.tokens) && view.tokens[i+1].Kind == lexer.RBrace) ||
		(i+2 < len(view.tokens) && view.tokens[i+2].Kind == lexer.RBrace) {
		return i, true
	}
	return 0, false
}

// isTypeArgPosition reports whether the cursor is in a generic or map argument
// list (`Page<|`, `map<K, |`); a touched `<` or `,` is mid.
func isTypeArgPosition(prev, mid *lexer.Token) bool {
	if mid != nil && (mid.Kind == lexer.LAngle || mid.Kind == lexer.Comma) {
		return true
	}
	return prev != nil && (prev.Kind == lexer.LAngle || prev.Kind == lexer.Comma)
}

// isFieldTypePosition reports whether the cursor is in a field's type slot
// (`name |`): prev is the name token of a parsed field.
func isFieldTypePosition(view snapshotView, c cursor) bool {
	if c.prev < 0 {
		return false
	}
	f := fieldAtCursor(view, c)
	return f != nil && f.Pos == view.tokens[c.prev].Pos
}

// isTypeParamDeclPosition reports whether the cursor is in a type parameter
// list being declared (`type Page<|`), not a generic argument (`Page<|`).
func isTypeParamDeclPosition(view snapshotView, c cursor) bool {
	i := c.prev
	if c.at >= 0 && (view.tokens[c.at].Kind == lexer.LAngle || view.tokens[c.at].Kind == lexer.Comma) {
		i = c.at
	}
	for ; i >= 2; i-- {
		switch view.tokens[i].Kind {
		case lexer.Ident, lexer.Comma:
			continue
		case lexer.LAngle:
			return view.tokens[i-1].Kind == lexer.Ident && view.tokens[i-2].Kind == lexer.KwType
		}
		return false
	}
	return false
}

// isScalarPrimitivePosition reports whether the cursor is in the primitive
// slot of `scalar Name |`.
func isScalarPrimitivePosition(view snapshotView, c cursor) bool {
	return c.prev >= 1 && view.tokens[c.prev].Kind == lexer.Ident && view.tokens[c.prev-1].Kind == lexer.KwScalar
}

// guessLevel returns the decorator site level of a `@` at the cursor.
func guessLevel(view snapshotView, c cursor) semantic.Level {
	if view.file == nil {
		return semantic.LvlFile
	}
	// At or above the `package` line the site is the file.
	if view.file.Package != nil && c.line <= view.file.Package.Pos.Line {
		return semantic.LvlFile
	}
	var prevDecl, nextDecl ast.Decl
	for _, d := range view.file.Decls {
		if d.DeclPos().Line >= c.line {
			if nextDecl == nil {
				nextDecl = d
			}
		} else {
			prevDecl = d
		}
	}
	if prevDecl != nil && cursorInsideDeclBody(view, c, prevDecl) {
		// Inside a type, enum, bodied error or service: its member level.
		switch v := prevDecl.(type) {
		case *ast.TypeDecl:
			return semantic.LvlField
		case *ast.EnumDecl:
			return semantic.LvlEnumValue
		case *ast.ErrorDecl:
			if v.HasBody {
				return semantic.LvlErrorField
			}
		case *ast.ServiceDecl:
			return semantic.LvlMethod
		}
	}
	if nextDecl != nil {
		// Above a declaration: that declaration's level.
		return declSiteLevel(nextDecl)
	}
	// A half-typed `@` swallows the next keyword as its name (`@service`), so the
	// declaration below is recovered from the tokens.
	if lvl := nextTopLevelDeclLevel(view, c); lvl != 0 {
		return lvl
	}
	// After the last declaration the site is the file.
	return semantic.LvlFile
}

// firstTopLevelDeclKeyword returns the first declaration keyword after the
// cursor at brace depth 0, or [lexer.EOF] when none follows or the cursor is
// inside a body.
func firstTopLevelDeclKeyword(view snapshotView, c cursor) lexer.Kind {
	depth := 0
	for _, t := range view.tokens {
		if t.Pos.Offset <= c.off {
			continue
		}
		switch t.Kind {
		case lexer.LBrace:
			depth++
			continue
		case lexer.RBrace:
			if depth == 0 {
				return lexer.EOF
			}
			depth--
			continue
		}
		if depth != 0 {
			continue
		}
		switch t.Kind {
		case lexer.KwType, lexer.KwEnum, lexer.KwError, lexer.KwScalar,
			lexer.KwService, lexer.KwExtend, lexer.KwMiddleware, lexer.KwEvent:
			return t.Kind
		}
	}
	return lexer.EOF
}

// nextTopLevelDeclLevel maps the next top-level decl keyword to its decorator
// site level, or 0 when none follows.
func nextTopLevelDeclLevel(view snapshotView, c cursor) semantic.Level {
	switch firstTopLevelDeclKeyword(view, c) {
	case lexer.KwType:
		return semantic.LvlType
	case lexer.KwEnum:
		return semantic.LvlEnum
	case lexer.KwError:
		return semantic.LvlError
	case lexer.KwScalar:
		return semantic.LvlScalar
	case lexer.KwEvent:
		return semantic.LvlEvent
	case lexer.KwService, lexer.KwExtend:
		return semantic.LvlService
	case lexer.KwMiddleware:
		return semantic.LvlMiddleware
	}
	return 0
}

// cursorInsideDeclBody reports whether the cursor is inside prev's braces,
// counted from prev's line since AST nodes carry no end position.
func cursorInsideDeclBody(view snapshotView, c cursor, prev ast.Decl) bool {
	if prev == nil {
		return false
	}
	startLine := prev.DeclPos().Line
	depth := 0
	for _, t := range view.tokens {
		if t.Pos.Line < startLine {
			continue
		}
		if t.Pos.Offset > c.off {
			break
		}
		switch t.Kind {
		case lexer.LBrace:
			depth++
		case lexer.RBrace:
			depth--
		}
	}
	return depth > 0
}

// declSiteLevel returns the decorator site level of d.
func declSiteLevel(d ast.Decl) semantic.Level {
	switch d.(type) {
	case *ast.TypeDecl:
		return semantic.LvlType
	case *ast.EnumDecl:
		return semantic.LvlEnum
	case *ast.ErrorDecl:
		return semantic.LvlError
	case *ast.ScalarDecl:
		return semantic.LvlScalar
	case *ast.EventDecl:
		return semantic.LvlEvent
	case *ast.MiddlewareDecl:
		return semantic.LvlMiddleware
	case *ast.ServiceDecl:
		return semantic.LvlService
	}
	return 0
}
