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

// onCompletion answers `textDocument/completion`. The strategy is
// context-driven, with a project-wide fallback so the user always sees
// declared types alongside keywords:
//
//  1. Inside a decorator (`@…`) → decorators filtered by site level.
//  2. Inside an `import "…"` literal → sibling packages from the
//     design root.
//  3. After a qualified prefix `pkg.…` → decls inside that package.
//  4. A type position - a generic or map argument, a `scalar Name`
//     primitive, a field's `name <cursor>` slot, or a `request` /
//     `response` / `payload` clause. Each offers the declaration kinds
//     ITS slot accepts, with the built-in primitives where one is legal.
//  5. Anywhere else → the keywords and declarations the ENCLOSING
//     BLOCK accepts (see [blockCompletions]); VSCode handles
//     client-side filtering by prefix.
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

// namedSlotCompletions answers the slots whose content is a NAME the
// project, the decorator registry or the design folder already knows -
// an import path, an extend target, a decorator or its arguments, a
// qualified reference, an error category, the file's package, a route's
// path parameter. Each is recognised by a token the user has already
// typed, so the walk is ordered by how specific that token is.
//
// The bool distinguishes "this slot is mine and the answer is nothing"
// from "not my slot"; only the latter falls through to the type-shape
// and block-fallback half in [Server.completionsAt].
func (s *Server) namedSlotCompletions(view snapshotView, pos protocol.Position, currentURI, currentSrc string, prev, mid *lexer.Token) ([]protocol.CompletionItem, bool) {
	if isInsideImportString(view, pos) {
		prefix := importStringPrefix(view, pos)
		return importPathCompletions(currentURI, prefix), true
	}
	// `import <cursor>` - the quotes are not typed yet, so the same
	// sibling-package list is offered with them included.
	if prev != nil && prev.Kind == lexer.KwImport {
		return quotedImportPathCompletions(currentURI), true
	}
	// After `extend service ` - list every primary service name in
	// the project so the user can pick which one this block extends.
	if isExtendServiceContext(view, pos) {
		return s.serviceNameCompletions(currentURI, currentSrc), true
	}
	// Inside a decorator argument list `@name(…|…)` - surface the
	// registered enum values, declared middleware names, or
	// security-scheme keys depending on the decorator + slot.
	// Must run before the qualified-ref check because the cursor sits
	// between `(` and `)` so the surrounding-token analysis would
	// otherwise route to the type-position branch.
	if name, ok := decoratorArgContext(view, pos); ok {
		if items := s.decoratorArgItems(view, pos, currentURI, currentSrc, name, prev, mid); items != nil {
			return items, true
		}
		// A registered decorator whose slot has no closed set takes a
		// free literal (`@doc("...")`, `@tags(users)`). Nothing useful
		// can be listed, and the block fallback below would answer with
		// declarations that are illegal inside parentheses - stay quiet.
		// An UNREGISTERED name means the context read is unreliable (a
		// stray `(` earlier in the buffer), so those fall through.
		if _, known := semantic.Registry[name]; known {
			return nil, true
		}
	}
	// Qualified ref `pkg.<cursor>` - list only the named package's
	// decls. Two cursor positions both qualify as "just after the
	// dot":
	//
	//   - Cursor on the dot itself (mid = Dot) - happens when the
	//     user has typed `shared.` and the next non-whitespace token
	//     starts on the next column. tokenAt's inclusive end-column
	//     check returns the dot.
	//   - Cursor on the identifier following the dot (mid = Ident,
	//     prev = Dot) - happens once the user starts typing the
	//     member name.
	//
	// Both must come BEFORE the decorator branch so an in-progress
	// `pkg.` shape is not mistaken for a decorator context.
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
	// Decorator name completion - cursor on (or right after) `@`, or
	// inside an identifier whose preceding token is `@`.
	if mid != nil && mid.Kind == lexer.At {
		return decoratorCompletions(view, pos, ""), true
	}
	if mid != nil && mid.Kind == lexer.Ident && prev != nil && prev.Kind == lexer.At {
		return decoratorCompletions(view, pos, mid.Text), true
	}
	// `error <Category>` position - fires when the cursor sits right
	// after the `error` keyword (mid is nil or the in-progress
	// category identifier). The 19 reserved HTTP categories are a
	// closed set, so completion is the obvious affordance.
	if prev != nil && prev.Kind == lexer.KwError && (mid == nil || mid.Kind == lexer.Ident) {
		return errorCategoryCompletions(), true
	}
	// `package <cursor>` - the file header. A design file's package is
	// not free-form: sibling files in the folder have already named it.
	if prev != nil && prev.Kind == lexer.KwPackage && (mid == nil || mid.Kind == lexer.Ident) {
		return s.packageNameCompletions(currentURI, currentSrc), true
	}
	// `/{<cursor>}` - a route's path parameter. Claiming it here is what
	// keeps [Server.completionsAt]'s just-opened-a-block rule from
	// reading the parameter's own brace as a block and answering
	// nothing.
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
	// Just opened a block - cursor right after `{` with no in-progress
	// identifier. A block whose members open with one of a short, closed
	// list of keys answers with that list: the keys ARE the affordance,
	// and there are at most seven. Every other block stays quiet (see
	// [blockOffersKeysWhenOpened]).
	//
	// `mid` may be the matching `}` (when the cursor sits inside an
	// empty `{}`), nil (cursor on whitespace), or absent. Anything
	// other than an in-progress identifier counts as "no signal yet".
	if prev != nil && prev.Kind == lexer.LBrace && (mid == nil || mid.Kind != lexer.Ident) {
		block := blockAt(view, pos)
		if !blockOffersKeysWhenOpened(block) {
			return nil
		}
		return s.blockKeyCompletions(block, currentURI, currentSrc)
	}
	// Field type suffix (`?` optional, `]` array close) - the legal
	// next token is either another type suffix, a decorator (`@...`),
	// or end-of-line. Returning keyword + project decls here would
	// spam noise; return empty so the popup stays out of the way
	// until the user types `@` (which the decorator branch above
	// handles).
	if prev != nil && (prev.Kind == lexer.Question || prev.Kind == lexer.RBracket) && (mid == nil || mid.Kind != lexer.Ident) {
		return nil
	}
	// `type Page<<cursor>>` - a type PARAMETER being declared, not a
	// type being referenced. The name is the author's to invent, so
	// nothing is offered; without this the `<` would read as the
	// generic-argument bracket it is everywhere else.
	if isTypeParamDeclPosition(view, pos) {
		return nil
	}
	// `request X` / `response X` / `payload X` - a method or event
	// clause, each of which names a message rather than any type.
	if prev != nil && (prev.Kind == lexer.KwRequest || prev.Kind == lexer.KwResponse || prev.Kind == lexer.KwPayload) {
		return s.clauseTypeCompletions(currentURI, currentSrc)
	}
	// Generic / map argument: builtins + every declared type
	// (project-wide).
	if isTypeArgPosition(prev, mid) {
		return s.typeCompletionsProjectWide(currentURI, currentSrc)
	}
	// `scalar Name <cursor>` - the primitive-type slot. The previous
	// token is the scalar name (Ident) so isTypePositionTrigger
	// would not fire on its own; check for the scalar-keyword two
	// tokens back to surface primitives in the position where they
	// are the ONLY legal next token.
	if isScalarPrimitivePosition(view, pos) {
		return scalarPrimitiveCompletions()
	}
	// `name <cursor>` - a field's type slot, which carries no separator
	// token for isTypePositionTrigger to key off.
	if isFieldTypePosition(view, pos, prev) {
		return s.typeCompletionsProjectWide(currentURI, currentSrc)
	}
	return s.blockCompletions(view, pos, currentURI, currentSrc)
}

// completionBlock names the syntactic block a cursor sits in. Each one
// accepts a different set of words, which is what [blockCompletions]
// turns into the fallback list.
type completionBlock uint8

const (
	// blockFile is file scope - the declaration keywords live here.
	blockFile completionBlock = iota
	// blockType is a `type` / `error` body: fields and mixins.
	blockType
	// blockEnum is an `enum` body: free-text value names only.
	blockEnum
	// blockService is a `service` / `extend service` body: HTTP methods.
	blockService
	// blockMethod is a method body: the request / response clauses.
	blockMethod
	// blockEvent is an `event` body: the payload clause.
	blockEvent
)

// blockAt classifies the block the cursor sits in from the declaration
// keyword that opened it and the brace depth reached at the cursor.
// Declaration bodies sit at depth 1; only a service nests further, so
// depth 2 inside one is a method body.
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

// fileKeywords / serviceKeywords / methodKeywords / eventKeywords are the
// reserved words each block accepts as the first word of a member. They
// are deliberately narrow: `map` reaches the user through the type
// position, `true` / `false` through a bool field's `@default`, and no
// block accepts the whole catalogue.
var (
	fileKeywords    = []string{"package", "import", "type", "enum", "error", "scalar", "service", "extend", "middleware", "event"}
	serviceKeywords = []string{"get", "post", "put", "patch", "delete", "head", "options"}
	methodKeywords  = []string{"request", "response"}
	eventKeywords   = []string{"payload"}
)

// blockCompletions is the fallback for a cursor the specific branches did
// not claim: whatever the enclosing block legally accepts there.
//
//   - file scope     → the declaration keywords
//   - type / error   → declared types, the only thing a mixin row may
//     name (a field NAME is free text, so nothing is offered for it)
//   - enum           → nothing; a value name is free text
//   - service        → the HTTP verbs
//   - method body    → `request` / `response`
//   - event body     → `payload`
func (s *Server) blockCompletions(view snapshotView, pos protocol.Position, currentURI, currentSrc string) []protocol.CompletionItem {
	return s.blockKeyCompletions(blockAt(view, pos), currentURI, currentSrc)
}

// blockKeyCompletions is [Server.blockCompletions] for a block already
// classified - the just-opened-a-brace branch has done that work.
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

// blockOffersKeysWhenOpened reports whether a cursor that has just
// opened b's brace is answered with b's keys rather than left silent.
//
// The three that are: a service body takes one of seven HTTP verbs, a
// method body `request` / `response`, an event body `payload`. Each set
// is closed, short, and the only thing that can legally start a member,
// so listing it is the affordance rather than noise.
//
// The rest stay silent. A `type` / `error` body opens on a field NAME,
// which is free text, and its one closed alternative - a mixin row - is
// every declared type in the project, the dump this rule was added to
// stop. An enum body is free text with no candidates at all. blockFile
// is also where an UNRECOGNISED `{` classifies, so the declaration
// keywords there would be wrong rather than merely noisy.
func blockOffersKeysWhenOpened(b completionBlock) bool {
	switch b {
	case blockService, blockMethod, blockEvent:
		return true
	}
	return false
}

// pathParamContext reports whether the cursor sits inside the `{…}` of a
// route path parameter, and returns the index of that opening brace.
//
// The shape it matches is the one [parser.parsePath] itself accepts - a
// `{` that follows a `/` and closes after at most one word - so a method
// body's opening brace, which never carries a bare `}` two tokens later,
// cannot be mistaken for one. `prev` is the brace when the parameter is
// still empty (`/{|}`, what an auto-closing editor leaves behind) and
// the partial name once the user has typed into it (`/{i|d}`).
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

// tokenIndex returns t's index in the view, or -1 when t is not one of
// its tokens. The callers hold a pointer handed out by
// [surroundingTokens], so identity is the match.
func tokenIndex(view snapshotView, t *lexer.Token) int {
	for i := range view.tokens {
		if &view.tokens[i] == t {
			return i
		}
	}
	return -1
}

// surroundingTokens returns the tokens immediately before and at the
// cursor. The "mid" token is the one whose span the cursor sits in
// (typically the identifier being typed); "prev" is the most recent
// non-trivia token whose span ends at or before the cursor.
//
// The position-aware backward scan is important: when the cursor sits
// on whitespace the lexer has no token there, but the LAST token in
// the file may be AFTER the cursor (e.g. cursor on the blank line
// between `{` and `}` of a multi-line block). Falling back to
// "last token in the slice" would mis-name `prev` as the trailing
// `}` and break every completion branch that keys off `prev.Kind`.
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

// scanFromIndex returns the token index to scan backward from for a completion
// at target: idx-1 when the cursor sits inside/after a token, otherwise the
// last non-EOF token that ends at or before target (-1 if none).
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

// posLessEq reports whether a comes at or before b in source order.
// Lines win the comparison; columns tie-break within the same line.
func posLessEq(a, b lexer.Position) bool {
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Column <= b.Column
}

// isTypeArgPosition reports whether the cursor sits in a generic or map
// argument list, where a type identifier - declared type OR built-in
// primitive - is the legal next token:
//
//   - `Page<X>` / `map<X, Y>`     - first argument, after the `<`
//   - `Pair<A, X>` / `map<K, X>`  - later arguments, after a `,`
//
// Both punctuators are read from `mid` as well as `prev` because
// tokenAt's inclusive end-column rule resolves a cursor touching one
// onto the punctuator itself: `map<|` arrives with the `<` as mid and
// `map` as prev.
//
// Colon is NOT a trigger: the grammar spells no type after one, and the
// two places it does appear - a decorator's `name: value` argument and
// an object literal's key - take a value. The clause keywords are not
// either; they name a struct, which is a narrower set
// ([Server.clauseTypeCompletions]). A field writes its type as
// `name X`, with no separator token at all - [isFieldTypePosition]
// recognises that slot.
func isTypeArgPosition(prev, mid *lexer.Token) bool {
	if mid != nil && (mid.Kind == lexer.LAngle || mid.Kind == lexer.Comma) {
		return true
	}
	return prev != nil && (prev.Kind == lexer.LAngle || prev.Kind == lexer.Comma)
}

// isFieldTypePosition reports whether the cursor sits in a field's type
// slot - the `<cursor>` of `name <cursor>` inside a `type` or `error`
// body, where both primitives and declared types are legal.
//
// The anchor is the parsed field whose NAME token is prev: the parser
// has already ruled on which rows are fields, so a mixin row, an enum
// value, a method clause, a declaration name and a bare `Ident` at file
// scope are all excluded without re-deriving the grammar here.
// Requiring the name token itself also keeps the slot AFTER a finished
// type (`name string <cursor>`, where the legal next token is a suffix,
// a decorator or the next member) out of the branch.
func isFieldTypePosition(view snapshotView, pos protocol.Position, prev *lexer.Token) bool {
	if prev == nil {
		return false
	}
	f := fieldAtCursor(view, pos)
	return f != nil && f.Pos == prev.Pos
}

// isTypeParamDeclPosition reports whether the cursor sits in the
// `<...>` of a type PARAMETER list on a `type` declaration - the slot
// that names a new parameter - rather than in the generic-argument list
// of a type being used. The two are spelled alike, so the opening `<`
// is walked back to and its owner checked: `type Name<` declares,
// anything else (`Page<`, `map<`) references.
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

// isScalarPrimitivePosition reports whether the cursor sits where a
// `scalar Name <cursor>` primitive-type ident is expected: the
// token two slots back is `scalar`, the immediately previous token
// is an ident (the scalar's name), and the cursor is past it.
// Catches `scalar Email <cursor>` so the IDE surfaces builtin
// primitives (`string`, `int`, ...) which is the ONLY legal next
// token at that position.
func isScalarPrimitivePosition(view snapshotView, pos protocol.Position) bool {
	idx, _ := view.tokenAt(pos.Line, pos.Character)
	// Walk backward from the cursor through non-trivia tokens looking
	// for the pattern `scalar <ident>` immediately preceding.
	target := lexer.Position{Line: int(pos.Line) + 1, Column: int(pos.Character) + 1}
	scanFrom := scanFromIndex(view, idx, target)
	// Need at least the ident + KwScalar pair before the cursor.
	if scanFrom < 1 {
		return false
	}
	prev := view.tokens[scanFrom]
	prevPrev := view.tokens[scanFrom-1]
	return prev.Kind == lexer.Ident && prevPrev.Kind == lexer.KwScalar
}

func guessLevel(view snapshotView, pos protocol.Position) semantic.Level {
	if view.file == nil {
		return semantic.LvlFile
	}
	line := int(pos.Line) + 1
	// File-header decorator zone: cursor sits AT or above the
	// `package` line - anything legal at file scope (`@version`,
	// `@doc`) wins. Without this branch the zone above
	// `package` would be classified by the first decl below it,
	// which is almost always wrong (a field-level decorator like
	// `@length` would surface as a completion for the file header).
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
		// Inside prev's body → field / method / enum value scope.
		// ErrorDecl without a body slot, ScalarDecl, and
		// MiddlewareDecl have no body to be inside; the brace-
		// depth check should already reject those, but we keep
		// the switch exhaustive so any future decl kind that
		// adds a body lands in the right bucket.
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
			// A service body holds HTTP methods and nothing else, so
			// every decorator zone inside one is a method site.
			return semantic.LvlMethod
		}
	}
	if nextDecl != nil {
		// Decorator zone - the cursor is in a blank stretch ABOVE
		// nextDecl, where every `@…` line ends up as a decorator
		// for that decl.
		return declSiteLevel(nextDecl)
	}
	// The AST lost the following declaration. While the user is mid-
	// typing a leading `@`, the parser swallows the next decl keyword as
	// the decorator's own name (`@service`, `@type`), so nextDecl is nil
	// even though a `service`/`type`/… keyword sits just below. The token
	// stream survives that corruption, so scan it for the next top-level
	// decl keyword and classify by that - otherwise the popup above a
	// `service` would offer only file-level decorators (no @prefix,
	// @group, @middlewares, @tags, @security).
	if lvl := nextTopLevelDeclLevel(view, pos); lvl != 0 {
		return lvl
	}
	// Trailing zone after the last decl. No syntactic owner; treat
	// as file scope so file-only decorators stay visible while
	// decl-only ones are correctly hidden.
	return semantic.LvlFile
}

// firstTopLevelDeclKeyword scans the token stream forward from pos for the next
// top-level declaration keyword (`type`, `service`, `extend`, ...) and returns
// its kind, or [lexer.EOF] when none follows. Brace depth is tracked so only a
// keyword at depth 0 - a real top-level decl, not an identifier named like a
// keyword inside some body - counts. The token stream survives the parse
// corruption a half-typed leading `@` causes (the keyword is swallowed as the
// decorator name), so this recovers the pending declaration the AST lost.
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
				// Cursor is inside an enclosing body, not a top-level
				// decorator zone - give up rather than guess.
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

// nextDeclDecoratorIsExtend reports whether the declaration the cursor's `@`
// zone precedes is an `extend service` block. An extend block accepts only the
// method-level-applicable service decorators plus @group - not @prefix - so the
// completion filter narrows the LvlService set accordingly.
func nextDeclDecoratorIsExtend(view snapshotView, pos protocol.Position) bool {
	return firstTopLevelDeclKeyword(view, pos) == lexer.KwExtend
}

// cursorInsideDeclBody walks the token stream from the start of
// prev until the cursor and tracks brace depth. A positive count
// means the cursor sits between an opening `{` and its matching
// `}` - i.e. inside the decl body - which is the only signal we
// have without explicit End positions on AST nodes.
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

// declSiteLevel maps a top-level declaration to the decorator-site
// bit it accepts. Used to filter the completion popup to decorators
// legal on the decl currently being authored.
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
