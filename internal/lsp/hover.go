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
	"LengthRequired": true, "PreconditionFailed": true, "PayloadTooLarge": true,
	"UnsupportedMediaType": true, "UnprocessableEntity": true, "Locked": true,
	"TooManyRequests": true, "Internal": true, "NotImplemented": true,
	"BadGateway": true, "ServiceUnavailable": true, "GatewayTimeout": true,
}

func isErrorCategory(s string) bool { return errorCategories[s] }

// isVerbToken reports whether t is a lexer-recognised HTTP verb
// keyword. Used by [hoverForToken] to gate the verb-doc dispatch so
// arbitrary idents that happen to be spelt "get" never surface the
// verb popup.
func isVerbToken(t lexer.Token) bool {
	switch t.Kind {
	case lexer.VerbGet, lexer.VerbPost, lexer.VerbPut, lexer.VerbPatch,
		lexer.VerbDelete, lexer.VerbHead, lexer.VerbOptions:
		return true
	}
	return false
}

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

// verbDocs documents the HTTP verb keywords so a hover on `get` /
// `post` / ... explains the semantic the framework attaches to it.
// Surfaced for keyword tokens that are recognised verbs - the same
// markdown a user would read in the language reference, scoped to
// the spot where they are about to commit a route to it.
var verbDocs = map[string]string{
	"get":     "**`get`** - safe, idempotent retrieval. The handler reads no body (the JSON decoder is skipped at codegen time).",
	"post":    "**`post`** - resource creation or non-idempotent action. JSON body decoded into the request struct.",
	"put":     "**`put`** - full resource replacement (idempotent). JSON body decoded into the request struct.",
	"patch":   "**`patch`** - partial update (non-idempotent unless the handler enforces it). JSON body decoded into the request struct.",
	"delete":  "**`delete`** - resource removal (idempotent). The handler reads no body.",
	"head":    "**`head`** - metadata-only retrieval. The handler returns headers without a body; codegen still binds path / query / header fields.",
	"options": "**`options`** - capability discovery (CORS preflight handler). The handler may return a custom Allow header set.",
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
	// HTTP verb keywords (`get`, `post`, ...) - the lexer assigns
	// these distinct Kw* token kinds, so dispatch by token text via
	// the verbDocs table.
	if doc, ok := verbDocs[tok.Text]; ok && isVerbToken(tok) {
		return &protocol.Hover{
			Contents: protocol.MarkupContent{Kind: protocol.Markdown, Value: doc},
			Range:    rangePtr(rangeOf(tok)),
		}
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
		// Field-name hover: when the ident is a field declared in
		// some type / error body, render its type + decorator chain so
		// the user can audit a field's contract without jumping to
		// the decl. Looked up by position so we only fire on the
		// definition site (not every occurrence of the same word).
		if f, parent := findFieldAtPos(view.file, tok.Pos); f != nil {
			return fieldHover(parent, f, rangeOf(tok))
		}
	}
	return nil
}

// findFieldAtPos walks every type / error body looking for a field
// whose declared name token starts at pos. Returns the field and the
// parent type / error name (for the hover header). Linear over body
// members - small bodies, infrequent calls; the cost is well within
// the LSP responsiveness budget.
func findFieldAtPos(f *ast.File, pos lexer.Position) (*ast.Field, string) {
	if f == nil {
		return nil, ""
	}
	for _, d := range f.Decls {
		switch v := d.(type) {
		case *ast.TypeDecl:
			for _, m := range v.Body {
				if fd, ok := m.(*ast.Field); ok && fd.Pos == pos {
					return fd, v.Name
				}
			}
		case *ast.ErrorDecl:
			for _, m := range v.Body {
				if fd, ok := m.(*ast.Field); ok && fd.Pos == pos {
					return fd, v.Name
				}
			}
		}
	}
	return nil, ""
}

// fieldHover renders the markdown popup for a field declaration: the
// owning type, the field's spelt-out type with optional / array
// markers, plus the decorator chain (one per line) so a reader scans
// the contract without leaving the cursor.
func fieldHover(parent string, f *ast.Field, r protocol.Range) *protocol.Hover {
	var sb strings.Builder
	if parent != "" {
		sb.WriteString("**field `")
		sb.WriteString(parent)
		sb.WriteByte('.')
		sb.WriteString(f.Name)
		sb.WriteString("`**\n\n")
	} else {
		sb.WriteString("**field `")
		sb.WriteString(f.Name)
		sb.WriteString("`**\n\n")
	}
	sb.WriteString("```craftgo\n")
	sb.WriteString(f.Name)
	sb.WriteByte(' ')
	sb.WriteString(typeRefString(f.Type))
	sb.WriteString("\n```\n")
	if len(f.Decorators) > 0 {
		sb.WriteString("\n**Decorators**\n")
		for _, d := range f.Decorators {
			sb.WriteString("- `@")
			sb.WriteString(d.Name)
			sb.WriteString("`\n")
		}
	}
	if len(f.Doc) > 0 {
		sb.WriteString("\n")
		sb.WriteString(strings.Join(f.Doc, "\n"))
	}
	return &protocol.Hover{
		Contents: protocol.MarkupContent{Kind: protocol.Markdown, Value: sb.String()},
		Range:    rangePtr(r),
	}
}

// typeRefString prints a TypeRef in source-style for hover output:
// `string`, `User[]`, `Page<User>?`, `map<string, int>`.
func typeRefString(t *ast.TypeRef) string {
	if t == nil {
		return "?"
	}
	var sb strings.Builder
	if t.Map != nil {
		sb.WriteString("map<")
		sb.WriteString(typeRefString(t.Map.Key))
		sb.WriteString(", ")
		sb.WriteString(typeRefString(t.Map.Value))
		sb.WriteByte('>')
	} else if t.Named != nil {
		sb.WriteString(t.Named.Name.String())
		if len(t.Named.Args) > 0 {
			sb.WriteByte('<')
			for i, a := range t.Named.Args {
				if i > 0 {
					sb.WriteString(", ")
				}
				sb.WriteString(typeRefString(a))
			}
			sb.WriteByte('>')
		}
	}
	if t.Array {
		sb.WriteString("[]")
	}
	for i := 1; i < t.ArrayDepth; i++ {
		sb.WriteString("[]")
	}
	if t.Optional {
		sb.WriteByte('?')
	}
	return sb.String()
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
