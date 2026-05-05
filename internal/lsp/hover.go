package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// errorCategories holds the reserved HTTP error category identifiers.
// The list mirrors the README §Keywords block - keep the two in sync.
var errorCategories = map[string]bool{
	"BadRequest": true, "Unauthorized": true, "PaymentRequired": true,
	"Forbidden": true, "NotFound": true, "MethodNotAllowed": true,
	"NotAcceptable": true, "Conflict": true, "Gone": true,
	"PreconditionFailed": true, "PayloadTooLarge": true,
	"UnsupportedMediaType": true, "UnprocessableEntity": true,
	"TooManyRequests": true, "Internal": true, "NotImplemented": true,
	"BadGateway": true, "ServiceUnavailable": true, "GatewayTimeout": true,
}

func isErrorCategory(s string) bool { return errorCategories[s] }

// builtinDocs is the doc table for the DSL's built-in primitives. It is
// kept here (rather than in semantic) because the body is hover-text:
// imperative, formatted markdown, opinionated, and likely to change as
// docs improve. Keep entries sorted alphabetically.
var builtinDocs = map[string]string{
	"any":     "**`any`** - opaque JSON value.\n\nGenerates `any` in Go.",
	"bool":    "**`bool`** - boolean primitive (`true` / `false`).",
	"bytes":   "**`bytes`** - raw byte buffer.\n\nGenerates `[]byte` in Go.",
	"file":    "**`file`** - multipart file upload (request only, must be paired with `@form`).\n\nGenerates `*multipart.FileHeader`.",
	"float32": "**`float32`** - 32-bit IEEE-754 float.",
	"float64": "**`float64`** - 64-bit IEEE-754 float.",
	"int":     "**`int`** - platform-sized signed integer.",
	"int8":    "**`int8`** - 8-bit signed integer.",
	"int16":   "**`int16`** - 16-bit signed integer.",
	"int32":   "**`int32`** - 32-bit signed integer.",
	"int64":   "**`int64`** - 64-bit signed integer.",
	"string":  "**`string`** - UTF-8 text primitive.",
	"uint":    "**`uint`** - platform-sized unsigned integer.",
	"uint8":   "**`uint8`** - 8-bit unsigned integer.",
	"uint16":  "**`uint16`** - 16-bit unsigned integer.",
	"uint32":  "**`uint32`** - 32-bit unsigned integer.",
	"uint64":  "**`uint64`** - 64-bit unsigned integer.",
}

// onHover answers `textDocument/hover`. It tokenises the buffer, finds
// the token under the cursor, and dispatches to a kind-specific renderer
// (decorator, builtin type, user type). Cursors that fall on whitespace,
// punctuation, or anywhere we have nothing to say return a nil result
// (LSP-spec for "no hover available here").
func (s *Server) onHover(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	var params protocol.HoverParams
	if err := json.Unmarshal(req.Params(), &params); err != nil {
		return reply(ctx, nil, err)
	}
	src := s.snapshot(params.TextDocument.URI)
	if src == "" {
		return reply(ctx, nil, nil)
	}
	view := parseSnapshot(string(params.TextDocument.URI), src)
	idx, tok := view.tokenAt(params.Position.Line, params.Position.Character)
	if idx < 0 {
		return reply(ctx, nil, nil)
	}
	hov := s.hoverWithProject(view, idx, tok, string(params.TextDocument.URI), src)
	return reply(ctx, hov, nil)
}

// hoverForToken classifies the token at idx and returns the hover popup
// or nil when nothing useful applies. It is exported as an internal
// helper so unit tests can exercise the formatting without spinning up
// a JSON-RPC stack.
func hoverForToken(view snapshotView, idx int, tok lexer.Token) *protocol.Hover {
	// `@name` decorators: the @ token sits at idx-1 (or idx itself when
	// the cursor lands on @). Inspect both.
	if tok.Kind == lexer.At && idx+1 < len(view.tokens) {
		next := view.tokens[idx+1]
		if next.Kind == lexer.Ident && view.tokens[idx+1].Pos.Line == tok.Pos.Line {
			return decoratorHover(next.Text, joinedRange(tok, next))
		}
	}
	if tok.Kind == lexer.Ident && idx > 0 && view.tokens[idx-1].Kind == lexer.At {
		return decoratorHover(tok.Text, joinedRange(view.tokens[idx-1], tok))
	}
	// Built-in types - only when the token spelling matches AND the
	// surrounding context is a type position (right after `request`,
	// `response`, `:`, a field name, etc.). The cheap heuristic: if it
	// is a bare Ident and the spelling is a known builtin, render it.
	if tok.Kind == lexer.Ident {
		if doc, ok := builtinDocs[tok.Text]; ok {
			return &protocol.Hover{
				Contents: protocol.MarkupContent{Kind: protocol.Markdown, Value: doc},
				Range:    rangePtr(rangeOf(tok)),
			}
		}
		if isErrorCategory(tok.Text) {
			return errorCategoryHover(tok)
		}
		if d := findDecl(view.file, tok.Text); d != nil {
			return userTypeHover(d, rangeOf(tok))
		}
	}
	return nil
}

// hoverWithProject extends [hoverForToken] with cross-package lookups.
// It is invoked from the LSP handler so the (slow) project walk only
// happens for hovers that did not resolve in the current file.
func (s *Server) hoverWithProject(view snapshotView, idx int, tok lexer.Token, currentURI string, currentSrc string) *protocol.Hover {
	if h := hoverForToken(view, idx, tok); h != nil {
		return h
	}
	if tok.Kind != lexer.Ident {
		return nil
	}
	qualified := qualifiedNameAt(view, idx)
	files, root := s.projectFilesWithRoot(uriToPath(currentURI), currentSrc)
	if d, _, ok := findDeclAcross(files, qualified, currentImports(view.file), root); ok {
		return userTypeHover(d, rangeOf(tok))
	}
	return nil
}

// decoratorHover renders the popup for `@name`. The body lists the
// allowed levels and the argument shape registered with the semantic
// analyser so editor and `craftgo lint` agree on what the decorator
// expects.
func decoratorHover(name string, r protocol.Range) *protocol.Hover {
	spec, ok := semantic.Registry[name]
	if !ok {
		return &protocol.Hover{
			Contents: protocol.MarkupContent{
				Kind:  protocol.Markdown,
				Value: fmt.Sprintf("**`@%s`** - unknown decorator.\n\nThe craftgo registry has no entry by this name; semantic analysis will report `decorator/unknown`.", name),
			},
			Range: rangePtr(r),
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**`@%s`** - %s\n\n", name, spec.Doc)
	if spec.Levels != 0 {
		fmt.Fprintf(&b, "_Allowed on:_ %s\n", spec.Levels.String())
	}
	if spec.Args.Min > 0 || spec.Args.Max != 0 {
		fmt.Fprintf(&b, "\n_Args:_ %s\n", argsRuleSummary(spec.Args))
	}
	if spec.AppliesTo != 0 {
		fmt.Fprintf(&b, "\n_Applies to:_ %s\n", spec.AppliesTo)
	}
	return &protocol.Hover{
		Contents: protocol.MarkupContent{Kind: protocol.Markdown, Value: b.String()},
		Range:    rangePtr(r),
	}
}

// argsRuleSummary turns the Min/Max/Kinds shape into a human-readable
// "1 string", "0..n", "1 ident or string" line for hover popups.
func argsRuleSummary(r semantic.ArgsRule) string {
	var arity string
	switch {
	case r.Max < 0:
		arity = fmt.Sprintf("%d..n", r.Min)
	case r.Min == r.Max:
		arity = fmt.Sprintf("%d", r.Min)
	default:
		arity = fmt.Sprintf("%d..%d", r.Min, r.Max)
	}
	if len(r.Kinds) == 0 && r.Variadic == 0 {
		return arity + " arg"
	}
	parts := make([]string, 0, len(r.Kinds))
	for _, k := range r.Kinds {
		parts = append(parts, k.String())
	}
	if r.Variadic != 0 {
		parts = append(parts, "..."+r.Variadic.String())
	}
	return arity + " (" + strings.Join(parts, ", ") + ")"
}

// userTypeHover formats the popup for a reference to a declared type,
// enum, error, scalar, middleware, or service. The signature line
// summarises the declaration; any doc comment follows below.
func userTypeHover(d ast.Decl, r protocol.Range) *protocol.Hover {
	header := declSummary(d)
	doc := strings.Join(declDoc(d), "\n")
	body := "```craftgo\n" + header + "\n```"
	if doc != "" {
		body += "\n\n" + doc
	}
	return &protocol.Hover{
		Contents: protocol.MarkupContent{Kind: protocol.Markdown, Value: body},
		Range:    rangePtr(r),
	}
}

// errorCategoryHover documents one of the reserved HTTP-status category
// names (BadRequest, NotFound, ...). The body is short - these are the
// well-known categories from the README.
func errorCategoryHover(tok lexer.Token) *protocol.Hover {
	body := fmt.Sprintf("**`%s`** - built-in error category.\n\nReserved name; use as `error %s YourErrorName` to declare an error of this kind.", tok.Text, tok.Text)
	return &protocol.Hover{
		Contents: protocol.MarkupContent{Kind: protocol.Markdown, Value: body},
		Range:    rangePtr(rangeOf(tok)),
	}
}

// joinedRange returns the LSP range covering the source span from a's
// first character through b's last character. Both tokens must be on
// the same line - used for `@name` (At + Ident).
func joinedRange(a, b lexer.Token) protocol.Range {
	end := b.Pos
	end.Column += len(b.Text)
	return protocol.Range{Start: lspPos(a.Pos), End: lspPos(end)}
}

func rangePtr(r protocol.Range) *protocol.Range { return &r }
