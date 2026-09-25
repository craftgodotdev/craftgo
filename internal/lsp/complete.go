package lsp

import (
	"context"

	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// onCompletion answers `textDocument/completion`; a document that is not open
// gets an empty list.
func (s *server) onCompletion(_ context.Context, params protocol.CompletionParams) (any, error) {
	r, ok := s.open(params.TextDocument.URI)
	if !ok {
		return &protocol.CompletionList{}, nil
	}
	return &protocol.CompletionList{Items: r.completionsAt(r.view().cursorAt(params.Position))}, nil
}

// namedSlotCompletions answers the slots holding a name the project, the registry
// or the design folder knows; the bool reports such a slot, even with no answer.
func (r *request) namedSlotCompletions(c cursor) ([]protocol.CompletionItem, bool) {
	view := r.view()
	prev, mid := view.token(c.prev), view.token(c.at)
	if prefix, ok := importPathPrefix(view, c); ok {
		return importPathCompletions(r.path, prefix), true
	}
	// `import |`: the paths come with their quotes.
	if prev != nil && prev.Kind == lexer.KwImport {
		return quotedImportPathCompletions(r.path), true
	}
	if isExtendServiceContext(view, c) {
		return r.serviceNameCompletions(), true
	}
	// `@name(|)`: the argument candidates of that decorator.
	if name, _, ok := decoratorArgContext(view, c); ok {
		if items := r.decoratorArgItems(c, name); items != nil {
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
			return r.packageDeclCompletions(pkg), true
		}
	}
	if prev != nil && prev.Kind == lexer.Dot {
		if pkg, ok := identBefore(view, c.prev); ok {
			return r.packageDeclCompletions(pkg), true
		}
	}
	// `@|` or `@na|`: decorator names.
	if mid != nil && mid.Kind == lexer.At {
		return r.decoratorCompletions(c, ""), true
	}
	if mid != nil && mid.Kind == lexer.Ident && prev != nil && prev.Kind == lexer.At {
		return r.decoratorCompletions(c, mid.Text), true
	}
	// `error |`: the error categories.
	if prev != nil && prev.Kind == lexer.KwError && (mid == nil || mid.Kind == lexer.Ident) {
		return errorCategoryCompletions(), true
	}
	// `package |`
	if prev != nil && prev.Kind == lexer.KwPackage && (mid == nil || mid.Kind == lexer.Ident) {
		return r.packageNameCompletions(), true
	}
	// `/{|}`: claimed here so the parameter's brace is not read as an opened block.
	if i, ok := pathParamContext(view, c); ok {
		return r.pathParamCompletions(i), true
	}
	return nil, false
}

// completionsAt returns the candidate items for the cursor c.
func (r *request) completionsAt(c cursor) []protocol.CompletionItem {
	if items, ok := r.namedSlotCompletions(c); ok {
		return items
	}
	view := r.view()
	prev, mid := view.token(c.prev), view.token(c.at)
	// Right after `{` with nothing typed (mid may be the closing `}`).
	if prev != nil && prev.Kind == lexer.LBrace && (mid == nil || mid.Kind != lexer.Ident) {
		block := blockAt(view, c)
		if !blockOffersKeysWhenOpened(block) {
			return nil
		}
		return r.blockKeyCompletions(block)
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
		return r.clauseTypeCompletions()
	}
	if isTypeArgPosition(prev, mid) {
		return r.typeCompletionsProjectWide()
	}
	if isScalarPrimitivePosition(view, c) {
		return scalarPrimitiveCompletions()
	}
	if isFieldTypePosition(view, c) {
		return r.typeCompletionsProjectWide()
	}
	return r.blockKeyCompletions(blockAt(view, c))
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

// declSite is what a declaration keyword opens: the decorator level of the
// declaration, the level of its body's members (0 when they take none) and
// the completion block of that body.
type declSite struct {
	self, member semantic.Level
	block        completionBlock
	// trailing: decorators after the head, on its line, are the declaration's.
	trailing bool
}

// declSites holds every declaration keyword.
var declSites = map[lexer.Kind]declSite{
	lexer.KwType:       {self: semantic.LvlType, member: semantic.LvlField, block: blockType},
	lexer.KwEnum:       {self: semantic.LvlEnum, member: semantic.LvlEnumValue, block: blockEnum},
	lexer.KwError:      {self: semantic.LvlError, member: semantic.LvlErrorField, block: blockType},
	lexer.KwScalar:     {self: semantic.LvlScalar, trailing: true},
	lexer.KwMiddleware: {self: semantic.LvlMiddleware},
	lexer.KwService:    {self: semantic.LvlService, member: semantic.LvlMethod, block: blockService},
	lexer.KwExtend:     {self: semantic.LvlService, member: semantic.LvlMethod, block: blockService},
	lexer.KwEvent:      {self: semantic.LvlEvent, block: blockEvent},
}

// isDeclKeyword reports whether k starts a declaration.
func isDeclKeyword(k lexer.Kind) bool {
	_, ok := declSites[k]
	return ok
}

// blockAt classifies the cursor's block by the enclosing declaration keyword
// and the brace depth; depth 2 inside a service is a method body.
func blockAt(view snapshotView, c cursor) completionBlock {
	kw, depth := enclosingDecl(view, c.lead())
	block := declSites[view.kind(kw)].block
	switch {
	case depth == 0:
		return blockFile
	case depth > 1 && block == blockService:
		return blockMethod
	}
	return block
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
func (r *request) blockKeyCompletions(block completionBlock) []protocol.CompletionItem {
	switch block {
	case blockType:
		return r.declCompletions(semantic.TypeDecls)
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

// guessLevel returns the decorator site level of a `@` at the cursor: inside a
// body the level of its members, after a trailing declaration's head on its
// line that declaration's, else that of the declaration below, or the file's
// above `package` and past the last declaration.
func guessLevel(view snapshotView, c cursor) semantic.Level {
	kw, depth := enclosingDecl(view, c.lead())
	site := declSites[view.kind(kw)]
	switch {
	case depth == 1:
		return site.member
	case depth > 1:
		return 0
	case site.trailing && view.tokens[kw].Pos.Line == c.line:
		return site.self
	}
	if next, ok := declSites[nextTopLevelKeyword(view, c)]; ok {
		return next.self
	}
	return semantic.LvlFile
}

// nextTopLevelKeyword returns the first `package` or declaration keyword after
// the cursor at brace depth 0, or [lexer.EOF] when none follows or the cursor
// is inside a body.
func nextTopLevelKeyword(view snapshotView, c cursor) lexer.Kind {
	depth := 0
	for i := range view.outsideParens(c.off) {
		t := view.tokens[i]
		switch {
		case t.Kind == lexer.LBrace:
			depth++
		case t.Kind == lexer.RBrace:
			if depth == 0 {
				return lexer.EOF
			}
			depth--
		case depth == 0 && (t.Kind == lexer.KwPackage || isDeclKeyword(t.Kind)):
			return t.Kind
		}
	}
	return lexer.EOF
}
