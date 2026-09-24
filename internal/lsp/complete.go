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
func (s *Server) onCompletion(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	var params protocol.CompletionParams
	if err := json.Unmarshal(req.Params(), &params); err != nil {
		return reply(ctx, nil, err)
	}
	src := s.snapshot(params.TextDocument.URI)
	if src == "" {
		return reply(ctx, &protocol.CompletionList{}, nil)
	}
	view := parseSnapshot(string(params.TextDocument.URI), src)
	items := s.completionsAt(view, params.Position, string(params.TextDocument.URI), src)
	return reply(ctx, &protocol.CompletionList{IsIncomplete: false, Items: items}, nil)
}

// namedSlotCompletions answers the slots holding a name the project, the registry
// or the design folder knows; the bool reports such a slot, even with no answer.
func (s *Server) namedSlotCompletions(view snapshotView, pos protocol.Position, currentURI, currentSrc string, prev, mid *lexer.Token) ([]protocol.CompletionItem, bool) {
	if isInsideImportString(view, pos) {
		prefix := importStringPrefix(view, pos)
		return importPathCompletions(currentURI, prefix), true
	}
	// `import |`: the paths come with their quotes.
	if prev != nil && prev.Kind == lexer.KwImport {
		return quotedImportPathCompletions(currentURI), true
	}
	if isExtendServiceContext(view, pos) {
		return s.serviceNameCompletions(currentURI, currentSrc), true
	}
	// `@name(|)`: the argument candidates of that decorator.
	if name, ok := decoratorArgContext(view, pos); ok {
		if items := s.decoratorArgItems(view, pos, currentURI, currentSrc, name, prev, mid); items != nil {
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
		if pkg, ok := identBefore(view, mid); ok {
			return s.packageDeclCompletions(currentURI, currentSrc, pkg), true
		}
	}
	if prev != nil && prev.Kind == lexer.Dot {
		if pkg, ok := identBefore(view, prev); ok {
			return s.packageDeclCompletions(currentURI, currentSrc, pkg), true
		}
	}
	// `@|` or `@na|`: decorator names.
	if mid != nil && mid.Kind == lexer.At {
		return decoratorCompletions(view, pos, ""), true
	}
	if mid != nil && mid.Kind == lexer.Ident && prev != nil && prev.Kind == lexer.At {
		return decoratorCompletions(view, pos, mid.Text), true
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
	if i, ok := pathParamContext(view, prev); ok {
		return s.pathParamCompletions(view, currentURI, currentSrc, i), true
	}
	return nil, false
}

// completionsAt returns the candidate items for a cursor in view.
func (s *Server) completionsAt(view snapshotView, pos protocol.Position, currentURI, currentSrc string) []protocol.CompletionItem {
	prev, mid := surroundingTokens(view, pos)
	if items, ok := s.namedSlotCompletions(view, pos, currentURI, currentSrc, prev, mid); ok {
		return items
	}
	// Right after `{` with nothing typed (mid may be the closing `}`).
	if prev != nil && prev.Kind == lexer.LBrace && (mid == nil || mid.Kind != lexer.Ident) {
		block := blockAt(view, pos)
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
	if isTypeParamDeclPosition(view, pos) {
		return nil
	}
	// `request |`, `response |` and `payload |` name a message type.
	if prev != nil && (prev.Kind == lexer.KwRequest || prev.Kind == lexer.KwResponse || prev.Kind == lexer.KwPayload) {
		return s.clauseTypeCompletions(currentURI, currentSrc)
	}
	if isTypeArgPosition(prev, mid) {
		return s.typeCompletionsProjectWide(currentURI, currentSrc)
	}
	if isScalarPrimitivePosition(view, pos) {
		return scalarPrimitiveCompletions()
	}
	if isFieldTypePosition(view, pos, prev) {
		return s.typeCompletionsProjectWide(currentURI, currentSrc)
	}
	return s.blockCompletions(view, pos, currentURI, currentSrc)
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
func blockAt(view snapshotView, pos protocol.Position) completionBlock {
	idx, _ := view.tokenAt(pos.Line, pos.Character)
	target := lexer.Position{Line: int(pos.Line) + 1, Column: int(pos.Character) + 1}
	kw, depth := enclosingDeclKeyword(view, scanFromIndex(view, idx, target)+1)
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

// blockCompletions offers what the enclosing block accepts at pos.
func (s *Server) blockCompletions(view snapshotView, pos protocol.Position, currentURI, currentSrc string) []protocol.CompletionItem {
	return s.blockKeyCompletions(blockAt(view, pos), currentURI, currentSrc)
}

// blockKeyCompletions offers what block accepts: the declared types a mixin
// can name in a type body, nothing in an enum, keywords elsewhere.
func (s *Server) blockKeyCompletions(block completionBlock, currentURI, currentSrc string) []protocol.CompletionItem {
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
func pathParamContext(view snapshotView, prev *lexer.Token) (int, bool) {
	i := tokenIndex(view, prev)
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

// tokenIndex returns the index of t, a pointer into view.tokens, or -1.
func tokenIndex(view snapshotView, t *lexer.Token) int {
	for i := range view.tokens {
		if &view.tokens[i] == t {
			return i
		}
	}
	return -1
}

// surroundingTokens returns the token before the cursor (prev) and the one
// under it (mid); on whitespace prev is the last token ending before it.
func surroundingTokens(view snapshotView, pos protocol.Position) (prev, mid *lexer.Token) {
	idx, _ := view.tokenAt(pos.Line, pos.Character)
	if idx >= 0 {
		mid = &view.tokens[idx]
	}
	target := lexer.Position{Line: int(pos.Line) + 1, Column: int(pos.Character) + 1}
	scanFrom := scanFromIndex(view, idx, target)
	for i := scanFrom; i >= 0; i-- {
		t := view.tokens[i]
		if t.Kind == lexer.EOF {
			continue
		}
		prev = &view.tokens[i]
		break
	}
	return prev, mid
}

// scanFromIndex returns the index to scan backward from: idx-1 when the cursor
// is on a token, else the last non-EOF token ending at or before target, or -1.
func scanFromIndex(view snapshotView, idx int, target lexer.Position) int {
	if idx >= 0 {
		return idx - 1
	}
	for i := len(view.tokens) - 1; i >= 0; i-- {
		t := view.tokens[i]
		if t.Kind == lexer.EOF {
			continue
		}
		end := t.Pos
		end.Column += len(t.Text)
		if posLessEq(end, target) {
			return i
		}
	}
	return -1
}

// posLessEq reports whether a is at or before b.
func posLessEq(a, b lexer.Position) bool {
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Column <= b.Column
}

// isTypeArgPosition reports whether the cursor is in a generic or map argument
// list (`Page<|`, `map<K, |`); tokenAt makes a touched `<` or `,` mid.
func isTypeArgPosition(prev, mid *lexer.Token) bool {
	if mid != nil && (mid.Kind == lexer.LAngle || mid.Kind == lexer.Comma) {
		return true
	}
	return prev != nil && (prev.Kind == lexer.LAngle || prev.Kind == lexer.Comma)
}

// isFieldTypePosition reports whether the cursor is in a field's type slot
// (`name |`): prev is the name token of a parsed field.
func isFieldTypePosition(view snapshotView, pos protocol.Position, prev *lexer.Token) bool {
	if prev == nil {
		return false
	}
	f := fieldAtCursor(view, pos)
	return f != nil && f.Pos == prev.Pos
}

// isTypeParamDeclPosition reports whether the cursor is in a type parameter
// list being declared (`type Page<|`), not a generic argument (`Page<|`).
func isTypeParamDeclPosition(view snapshotView, pos protocol.Position) bool {
	idx, _ := view.tokenAt(pos.Line, pos.Character)
	target := lexer.Position{Line: int(pos.Line) + 1, Column: int(pos.Character) + 1}
	i := scanFromIndex(view, idx, target)
	if idx >= 0 && idx < len(view.tokens) &&
		(view.tokens[idx].Kind == lexer.LAngle || view.tokens[idx].Kind == lexer.Comma) {
		i = idx
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
func isScalarPrimitivePosition(view snapshotView, pos protocol.Position) bool {
	idx, _ := view.tokenAt(pos.Line, pos.Character)
	target := lexer.Position{Line: int(pos.Line) + 1, Column: int(pos.Character) + 1}
	scanFrom := scanFromIndex(view, idx, target)
	if scanFrom < 1 {
		return false
	}
	prev := view.tokens[scanFrom]
	prevPrev := view.tokens[scanFrom-1]
	return prev.Kind == lexer.Ident && prevPrev.Kind == lexer.KwScalar
}

// guessLevel returns the decorator site level of a `@` at pos.
func guessLevel(view snapshotView, pos protocol.Position) semantic.Level {
	if view.file == nil {
		return semantic.LvlFile
	}
	line := int(pos.Line) + 1
	// At or above the `package` line the site is the file.
	if view.file.Package != nil && line <= view.file.Package.Pos.Line {
		return semantic.LvlFile
	}
	var prevDecl, nextDecl ast.Decl
	for _, d := range view.file.Decls {
		if d.DeclPos().Line >= line {
			if nextDecl == nil {
				nextDecl = d
			}
		} else {
			prevDecl = d
		}
	}
	if prevDecl != nil && cursorInsideDeclBody(view, pos, prevDecl) {
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
	if lvl := nextTopLevelDeclLevel(view, pos); lvl != 0 {
		return lvl
	}
	// After the last declaration the site is the file.
	return semantic.LvlFile
}

// firstTopLevelDeclKeyword returns the first declaration keyword after pos at
// brace depth 0, or [lexer.EOF] when none follows or pos is inside a body.
func firstTopLevelDeclKeyword(view snapshotView, pos protocol.Position) lexer.Kind {
	cursorLine := int(pos.Line) + 1
	cursorCol := int(pos.Character) + 1
	depth := 0
	for _, t := range view.tokens {
		if t.Pos.Line < cursorLine || (t.Pos.Line == cursorLine && t.Pos.Column <= cursorCol) {
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
func nextTopLevelDeclLevel(view snapshotView, pos protocol.Position) semantic.Level {
	switch firstTopLevelDeclKeyword(view, pos) {
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

// nextDeclDecoratorIsExtend reports whether the declaration after pos is an
// `extend service`.
func nextDeclDecoratorIsExtend(view snapshotView, pos protocol.Position) bool {
	return firstTopLevelDeclKeyword(view, pos) == lexer.KwExtend
}

// cursorInsideDeclBody reports whether pos is inside prev's braces, counted
// from prev's line since AST nodes carry no end position.
func cursorInsideDeclBody(view snapshotView, pos protocol.Position, prev ast.Decl) bool {
	if prev == nil {
		return false
	}
	cursorLine := int(pos.Line) + 1
	cursorCol := int(pos.Character) + 1
	startLine := prev.DeclPos().Line
	depth := 0
	for _, t := range view.tokens {
		if t.Pos.Line < startLine {
			continue
		}
		if t.Pos.Line > cursorLine || (t.Pos.Line == cursorLine && t.Pos.Column > cursorCol) {
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
