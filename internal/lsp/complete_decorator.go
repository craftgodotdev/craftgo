// Decorator-arg/name LSP completions: dispatcher, context detection, error categories.
package lsp

import (
	"fmt"
	"sort"
	"strings"

	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

func (s *Server) decoratorArgItems(view snapshotView, pos protocol.Position, currentURI, currentSrc, name string, prev, mid *lexer.Token) []protocol.CompletionItem {
	if name == "middlewares" {
		return s.middlewareNameCompletions(currentURI, currentSrc)
	}
	if name == "security" {
		if items := s.securitySchemeCompletions(currentURI); items != nil {
			return items
		}
	}
	if name == "default" {
		if items := s.defaultEnumCompletions(view, pos, currentURI, currentSrc); items != nil {
			return items
		}
	}
	if spec, ok := semantic.Registry[name]; ok && len(spec.Args.Kinds) > 0 {
		switch spec.Args.Kinds[0] {
		case semantic.ArgDuration:
			return durationCompletions(prev, mid)
		case semantic.ArgSize:
			return sizeCompletions(prev, mid)
		}
	}
	return decoratorArgCompletions(view, pos, name)
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

func decoratorArgContext(view snapshotView, pos protocol.Position) (string, bool) {
	idx, _ := view.tokenAt(pos.Line, pos.Character)
	if idx < 0 {
		idx = len(view.tokens)
	}
	depth := 0
	for i := idx; i >= 0; i-- {
		if i >= len(view.tokens) {
			continue
		}
		t := view.tokens[i]
		switch t.Kind {
		case lexer.RParen:
			// Skip the cursor's own RParen - we're INSIDE its
			// decorator, not after it.
			if i == idx {
				continue
			}
			depth++
		case lexer.LParen:
			if depth > 0 {
				depth--
				continue
			}
			// Found the unmatched `(`. Look two tokens back for
			// `@<ident>`. The Ident may be either a plain identifier
			// or one of the keyword-spelt decorators (`@true`).
			if i >= 2 && view.tokens[i-2].Kind == lexer.At {
				return view.tokens[i-1].Text, true
			}
			return "", false
		}
	}
	return "", false
}

// defaultEnumCompletions returns the enum's value names when the
// cursor sits inside `@default(...)` on a field whose declared type
// (or array-element type) is an enum reachable from the current
// package. Lookup spans every sibling file in the project so
// multi-file packages where the enum lives in a separate
// `*.craftgo` file still surface the values. Returns nil when the
// field type isn't an enum so the caller falls through.

func decoratorArgCompletions(view snapshotView, pos protocol.Position, name string) []protocol.CompletionItem {
	spec, ok := semantic.Registry[name]
	if !ok {
		return nil
	}
	_ = view
	_ = pos
	values := spec.Args.Enum
	if len(values) == 0 {
		return nil
	}
	out := make([]protocol.CompletionItem, 0, len(values))
	for _, v := range values {
		out = append(out, protocol.CompletionItem{
			Label:      v,
			Kind:       protocol.CompletionItemKindEnumMember,
			Detail:     "@" + name + " value",
			InsertText: v,
		})
	}
	return out
}

// isExtendServiceContext reports whether the cursor sits at the
// identifier slot of an `extend service <cursor>` clause. The check
// walks tokens backwards: if the two most recent non-cursor tokens
// (skipping any partial ident the user is typing) are `service` then
// `extend`, we are at the slot.
//
// Boundary handling: when the cursor sits past the last real token
// (tokenAt returned -1 because EOF is the only thing left),
// `idx == len(view.tokens)` and we must NOT index into the slice.
// Likewise the partial-ident skip needs to verify `idx` is in range
// before reading `view.tokens[idx]`.

func decoratorCompletions(view snapshotView, pos protocol.Position, prefix string) []protocol.CompletionItem {
	level := guessLevel(view, pos)
	// At field level, narrow further by the field's primitive type
	// so a `total int? @<cursor>` does not surface string-only or
	// array-only validators. Returns 0 (PrimAny) when the cursor is
	// not on a field row, in which case the AppliesTo filter is a
	// no-op and only the level filter applies.
	var fieldPrim semantic.Prims
	if level == semantic.LvlField {
		fieldPrim = fieldPrimAt(view, pos)
	}
	names := make([]string, 0, len(semantic.Registry))
	for name := range semantic.Registry {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]protocol.CompletionItem, 0, len(names))
	for _, name := range names {
		spec := semantic.Registry[name]
		// Strict level filter: only surface decorators whose
		// declared site mask intersects the cursor's level. The
		// guard against `spec.Levels == 0` is defensive for any
		// future Registry entry without a Levels declaration -
		// treating "no levels" as "not applicable here" keeps the
		// completion list focused on supported decorators.
		if spec.Levels == 0 || spec.Levels&level == 0 {
			continue
		}
		// Per-primitive filter: at field level, drop validators
		// whose AppliesTo doesn't intersect the field's resolved
		// primitive. Decorators with AppliesTo == 0 (PrimAny) pass
		// through - they apply regardless of type.
		if fieldPrim != 0 && spec.AppliesTo != 0 && spec.AppliesTo&fieldPrim == 0 {
			continue
		}
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			continue
		}
		insert := name
		if needsArgs(spec.Args) {
			insert = name + "($0)"
		}
		out = append(out, protocol.CompletionItem{
			Label:            name,
			Kind:             protocol.CompletionItemKindFunction,
			Detail:           argsRuleSummary(spec.Args),
			Documentation:    spec.Doc,
			InsertText:       insert,
			InsertTextFormat: protocol.InsertTextFormatSnippet,
		})
	}
	return out
}

// needsArgs reports whether the decorator REQUIRES arguments - only
// then do we expand the completion item into `name($0)` so the cursor
// lands inside the parens. Decorators where args are optional (Min=0)
// like `@deprecated` should insert bare so the user can accept the
// no-arg form without deleting empty parentheses.
func needsArgs(r semantic.ArgsRule) bool {
	return r.Min > 0 || r.Variadic != 0
}

var errorCategoryStatus = []struct {
	name   string
	status int
}{
	{"BadRequest", 400},
	{"Unauthorized", 401},
	{"PaymentRequired", 402},
	{"Forbidden", 403},
	{"NotFound", 404},
	{"MethodNotAllowed", 405},
	{"NotAcceptable", 406},
	{"Conflict", 409},
	{"Gone", 410},
	{"LengthRequired", 411},
	{"PreconditionFailed", 412},
	{"PayloadTooLarge", 413},
	{"UnsupportedMediaType", 415},
	{"UnprocessableEntity", 422},
	{"Locked", 423},
	{"TooManyRequests", 429},
	{"Internal", 500},
	{"NotImplemented", 501},
	{"BadGateway", 502},
	{"ServiceUnavailable", 503},
	{"GatewayTimeout", 504},
}

// errorCategoryCompletions returns one completion item per reserved
// HTTP error category. Fired when the cursor sits in the
// `error <cursor>` position. Each item carries the HTTP status as
// Detail and a short doc snippet that the LSP client can render in
// the autocomplete popup.
func errorCategoryCompletions() []protocol.CompletionItem {
	out := make([]protocol.CompletionItem, 0, len(errorCategoryStatus))
	for _, c := range errorCategoryStatus {
		detail := fmt.Sprintf("HTTP %d", c.status)
		doc := protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: fmt.Sprintf("**`%s`** - built-in error category (HTTP %d).\n\nUse as `error %s YourErrorName` to declare an error of this kind.", c.name, c.status, c.name),
		}
		out = append(out, protocol.CompletionItem{
			Label:         c.name,
			Kind:          protocol.CompletionItemKindEnumMember,
			Detail:        detail,
			Documentation: doc,
		})
	}
	return out
}
