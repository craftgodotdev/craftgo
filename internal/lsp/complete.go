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
//  4. After `request` / `response` / `:` / `<` / `,` (a type position)
//     → declared types (project-wide) + built-in primitives.
//  5. Anywhere else → keywords + project-wide types as a single
//     blended list. VSCode handles client-side filtering by prefix.

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

// completionsAt returns the candidate items for a cursor in view.
func (s *Server) completionsAt(view snapshotView, pos protocol.Position, currentURI, currentSrc string) []protocol.CompletionItem {
	prev, mid := surroundingTokens(view, pos)
	// Inside an import string literal (`import "…|"`) - suggest
	// available package paths.
	if isInsideImportString(view, pos) {
		prefix := importStringPrefix(view, pos)
		return s.importPathCompletions(currentURI, currentSrc, prefix)
	}
	// After `extend service ` - list every primary service name in
	// the project so the user can pick which one this block extends.
	if isExtendServiceContext(view, pos) {
		return s.serviceNameCompletions(currentURI, currentSrc)
	}
	// Inside a decorator argument list `@name(…|…)` - surface the
	// registered enum values, declared middleware names, or
	// security-scheme keys depending on the decorator + slot.
	// Must run before the qualified-ref check because the cursor sits
	// between `(` and `)` so the surrounding-token analysis would
	// otherwise route to the type-position branch.
	if name, ok := decoratorArgContext(view, pos); ok {
		if items := s.decoratorArgItems(view, pos, currentURI, currentSrc, name, prev, mid); items != nil {
			return items
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
			return s.packageDeclCompletions(view, currentURI, currentSrc, pkg)
		}
	}
	if prev != nil && prev.Kind == lexer.Dot {
		if pkg, ok := identBefore(view, prev); ok {
			return s.packageDeclCompletions(view, currentURI, currentSrc, pkg)
		}
	}
	// Decorator name completion - cursor on (or right after) `@`, or
	// inside an identifier whose preceding token is `@`.
	if mid != nil && mid.Kind == lexer.At {
		return decoratorCompletions(view, pos, "")
	}
	if mid != nil && mid.Kind == lexer.Ident && prev != nil && prev.Kind == lexer.At {
		return decoratorCompletions(view, pos, mid.Text)
	}
	// `error <Category>` position - fires when the cursor sits right
	// after the `error` keyword (mid is nil or the in-progress
	// category identifier). The 19 reserved HTTP categories are a
	// closed set, so completion is the obvious affordance.
	if prev != nil && prev.Kind == lexer.KwError && (mid == nil || mid.Kind == lexer.Ident) {
		return errorCategoryCompletions()
	}
	// Just opened a block - cursor right after `{` with no
	// in-progress identifier. Auto-suggest here is purely noise: the
	// user has not signalled what they're about to type, and the
	// project-wide-decls dump shadows whatever they actually wanted.
	// Return empty so VS Code's popup stays out of the way; users who
	// invoke completion manually (or start typing a letter) will land
	// in the regular branches below.
	//
	// `mid` may be the matching `}` (when the cursor sits inside an
	// empty `{}`), nil (cursor on whitespace), or absent. Anything
	// other than an in-progress identifier counts as "no signal yet".
	if prev != nil && prev.Kind == lexer.LBrace && (mid == nil || mid.Kind != lexer.Ident) {
		return nil
	}
	// Type position: include builtins + every declared type
	// (project-wide).
	if prev != nil && isTypePositionTrigger(*prev) {
		return s.typeCompletionsProjectWide(view, currentURI, currentSrc)
	}
	// General context - keywords + project-wide declared types so
	// users typing identifiers see what they have already defined.
	items := keywordCompletions()
	items = append(items, s.declCompletionsProjectWide(view, currentURI, currentSrc)...)
	return items
}

// decoratorArgItems dispatches a decorator-argument completion to
// the right resolver based on which decorator the cursor sits in.
// Special-cased decorators:
//
//   - `@middlewares(...)` → declared middleware names.
//   - `@security(A, B, ...)` → keys declared in the project's
//     `openapi.securitySchemes` (any slot, since the decorator is a
//     variadic ident list).
//   - `@default(...)` → enum values when the field's type is an enum.
//   - everything else → the registered enum values from the
//     decorator's [semantic.Spec].
//
// Returns nil when none of the slots match - the caller falls back
// to its general-context branch.

func surroundingTokens(view snapshotView, pos protocol.Position) (prev, mid *lexer.Token) {
	idx, _ := view.tokenAt(pos.Line, pos.Character)
	if idx >= 0 {
		mid = &view.tokens[idx]
	}
	target := lexer.Position{Line: int(pos.Line) + 1, Column: int(pos.Character) + 1}
	scanFrom := idx - 1
	if idx < 0 {
		scanFrom = -1
		for i := len(view.tokens) - 1; i >= 0; i-- {
			t := view.tokens[i]
			if t.Kind == lexer.EOF {
				continue
			}
			end := t.Pos
			end.Column += len(t.Text)
			if posLessEq(end, target) {
				scanFrom = i
				break
			}
		}
	}
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

// posLessEq reports whether a comes at or before b in source order.
// Lines win the comparison; columns tie-break within the same line.
func posLessEq(a, b lexer.Position) bool {
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Column <= b.Column
}

func isTypePositionTrigger(t lexer.Token) bool {
	switch t.Kind {
	case lexer.KwRequest, lexer.KwResponse,
		lexer.Colon, lexer.LAngle, lexer.Comma:
		return true
	}
	return false
}

// isInsideImportString reports whether pos lies inside an `import "…"`
// string literal - the cursor sits between the two double-quotes that
// follow an `import` keyword. We rely on token-level inspection rather
// than re-lexing the partial line because the editor may send a cursor
// position that splits a token mid-string.

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
			return semantic.LvlMethod
		}
	}
	if nextDecl != nil {
		// Decorator zone - the cursor is in a blank stretch ABOVE
		// nextDecl, where every `@…` line ends up as a decorator
		// for that decl.
		return declSiteLevel(nextDecl)
	}
	// Trailing zone after the last decl. No syntactic owner; treat
	// as file scope so file-only decorators stay visible while
	// decl-only ones are correctly hidden.
	return semantic.LvlFile
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
	case *ast.MiddlewareDecl:
		return semantic.LvlMiddleware
	case *ast.ServiceDecl:
		return semantic.LvlService
	}
	return 0
}
