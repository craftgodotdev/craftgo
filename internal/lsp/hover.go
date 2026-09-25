package lsp

import (
	"context"
	"fmt"
	"strings"

	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/errcat"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// verbDocs is the hover text of each HTTP verb keyword.
var verbDocs = map[string]string{
	"get":     "**`get`** - safe, idempotent retrieval. The handler reads no body (the JSON decoder is skipped at codegen time).",
	"post":    "**`post`** - resource creation or non-idempotent action. JSON body decoded into the request struct.",
	"put":     "**`put`** - full resource replacement (idempotent). JSON body decoded into the request struct.",
	"patch":   "**`patch`** - partial update (non-idempotent unless the handler enforces it). JSON body decoded into the request struct.",
	"delete":  "**`delete`** - resource removal (idempotent). The handler reads no body.",
	"head":    "**`head`** - metadata-only retrieval. The handler returns headers without a body; codegen still binds path / query / header fields.",
	"options": "**`options`** - capability discovery (CORS preflight handler). The handler may return a custom Allow header set.",
}

// keywordDoc is a clause keyword's hover text and the declaration keyword it
// must sit under, so a field named `payload` gets none.
type keywordDoc struct {
	site lexer.Kind
	doc  string
}

// memberKeywordDocs documents `event`, `payload`, `request` and `response`.
var memberKeywordDocs = map[lexer.Kind]keywordDoc{
	lexer.KwEvent:    {lexer.KwEvent, "**`event Name { payload Type }`** - a contract this design declares, at file level: a service publishes HTTP, never events. Codegen emits one descriptor for it - publish and subscribe both go through that; transport, codec and which deployable listens are runtime wiring, not part of the contract."},
	lexer.KwPayload:  {lexer.KwEvent, "**`payload Type`** - the type an event contract carries. Must name a `type` declaration."},
	lexer.KwRequest:  {lexer.KwService, "**`request Type`** - the type a method binds and validates from the request."},
	lexer.KwResponse: {lexer.KwService, "**`response Type`** - the type a method returns; the framework encodes it."},
}

// memberKeywordHover renders a clause keyword's doc when it sits in its own
// declaration; an `extend service` counts as a service.
func memberKeywordHover(view snapshotView, idx int, tok lexer.Token) *protocol.Hover {
	kd, ok := memberKeywordDocs[tok.Kind]
	if !ok {
		return nil
	}
	kw, _ := enclosingDecl(view, idx+1)
	site := view.kind(kw)
	if site == lexer.KwExtend {
		site = lexer.KwService
	}
	if site != kd.site {
		return nil
	}
	return &protocol.Hover{
		Contents: protocol.MarkupContent{Kind: protocol.Markdown, Value: kd.doc},
		Range:    rangePtr(rangeOf(view.src, tok)),
	}
}

// onHover answers `textDocument/hover`, with null where there is nothing to show.
func (s *server) onHover(_ context.Context, params protocol.HoverParams) (any, error) {
	r, ok := s.open(params.TextDocument.URI)
	if !ok {
		return nil, nil
	}
	c := r.view().cursorAt(params.Position)
	if c.at < 0 {
		return nil, nil
	}
	return r.hover(c.at), nil
}

// hoverForToken returns the hover for token idx from the buffer alone, or nil.
func hoverForToken(view snapshotView, idx int, tok lexer.Token) *protocol.Hover {
	// `@name`: the cursor is on the `@` or on the name.
	if tok.Kind == lexer.At && idx+1 < len(view.tokens) {
		next := view.tokens[idx+1]
		if next.Kind == lexer.Ident && view.tokens[idx+1].Pos.Line == tok.Pos.Line {
			return decoratorHover(next.Text, joinedRange(view.src, tok, next))
		}
	}
	if tok.Kind == lexer.Ident && idx > 0 && view.tokens[idx-1].Kind == lexer.At {
		return decoratorHover(tok.Text, joinedRange(view.src, view.tokens[idx-1], tok))
	}
	if h := formatRawArgHover(view, idx, tok); h != nil {
		return h
	}
	if doc, ok := verbDocs[tok.Text]; ok && tok.Kind.IsVerb() {
		return &protocol.Hover{
			Contents: protocol.MarkupContent{Kind: protocol.Markdown, Value: doc},
			Range:    rangePtr(rangeOf(view.src, tok)),
		}
	}
	if h := memberKeywordHover(view, idx, tok); h != nil {
		return h
	}
	// Any identifier spelt like a built-in gets its doc.
	if tok.Kind == lexer.Ident {
		if sp, ok := prims.Lookup(tok.Text); ok {
			return &protocol.Hover{
				Contents: protocol.MarkupContent{Kind: protocol.Markdown, Value: sp.Doc},
				Range:    rangePtr(rangeOf(view.src, tok)),
			}
		}
		if errcat.IsCategory(tok.Text) {
			return errorCategoryHover(tok.Text, rangeOf(view.src, tok))
		}
		// A field's own name token shows the field.
		if f, parent := findFieldAtPos(view.file, tok.Pos); f != nil {
			return fieldHover(parent, f, rangeOf(view.src, tok))
		}
	}
	return nil
}

// formatRawArgHover renders the hover for `raw` in `@format(raw)`, the @format
// value that changes the field's Go type.
func formatRawArgHover(view snapshotView, idx int, tok lexer.Token) *protocol.Hover {
	if tok.Kind != lexer.Ident || tok.Text != semantic.FormatRaw || idx < 3 {
		return nil
	}
	if view.tokens[idx-1].Kind != lexer.LParen ||
		view.tokens[idx-2].Text != "format" ||
		view.tokens[idx-3].Kind != lexer.At {
		return nil
	}
	return &protocol.Hover{
		Contents: protocol.MarkupContent{Kind: protocol.Markdown, Value: semantic.FormatRawDoc},
		Range:    rangePtr(rangeOf(view.src, tok)),
	}
}

// findFieldAtPos returns the field whose name token starts at pos, with the
// name of its type or error.
func findFieldAtPos(f *ast.File, pos lexer.Position) (*ast.Field, string) {
	if f == nil {
		return nil, ""
	}
	for _, d := range f.Decls {
		body, ok := declBody(d)
		if !ok {
			continue
		}
		for _, m := range body {
			if fd, ok := m.(*ast.Field); ok && fd.Pos == pos {
				return fd, d.DeclName()
			}
		}
	}
	return nil, ""
}

// fieldHover renders a field's owner, type, decorators and doc.
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
	sb.WriteString(f.Type.String())
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

// hover is [hoverForToken] for token idx, else the declaration the token
// names; the project loads only when the buffer has no answer.
func (r *request) hover(idx int) *protocol.Hover {
	view := r.view()
	tok := view.tokens[idx]
	if h := hoverForToken(view, idx, tok); h != nil {
		return h
	}
	if d := r.project().symbolAt(view, idx); d != nil {
		return userTypeHover(d, rangeOf(view.src, tok))
	}
	return nil
}

// decoratorHover renders `@name` from its registry entry, or says the name is
// removed or unknown.
func decoratorHover(name string, r protocol.Range) *protocol.Hover {
	spec, ok := semantic.DecoratorSpec(name)
	if !ok {
		if note, gone := semantic.RemovedDecorator(name); gone {
			return &protocol.Hover{
				Contents: protocol.MarkupContent{
					Kind:  protocol.Markdown,
					Value: fmt.Sprintf("**`@%s`** - removed decorator.\n\n%s\n\nSemantic analysis reports `decorator/removed`.", name, note),
				},
				Range: rangePtr(r),
			}
		}
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

// argsRuleSummary renders an argument rule as "1 (string)", "0..n arg" or
// "1..2 (int, int)".
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

// userTypeHover renders d's declaration line and doc.
func userTypeHover(d ast.Decl, r protocol.Range) *protocol.Hover {
	info := infoOf(d)
	body := "```craftgo\n" + info.summary + "\n```"
	if doc := strings.Join(info.doc, "\n"); doc != "" {
		body += "\n\n" + doc
	}
	return &protocol.Hover{
		Contents: protocol.MarkupContent{Kind: protocol.Markdown, Value: body},
		Range:    rangePtr(r),
	}
}

// errorCategoryHover renders the hover of the error category name at r.
func errorCategoryHover(name string, r protocol.Range) *protocol.Hover {
	body := fmt.Sprintf("**`%s`** - built-in error category.\n\nReserved name; use as `error %s YourErrorName` to declare an error of this kind.", name, name)
	return &protocol.Hover{
		Contents: protocol.MarkupContent{Kind: protocol.Markdown, Value: body},
		Range:    rangePtr(r),
	}
}

// joinedRange returns the range from a's start to b's end, tokens of src.
func joinedRange(src string, a, b lexer.Token) protocol.Range {
	return protocol.Range{Start: rangeOf(src, a).Start, End: rangeOf(src, b).End}
}

func rangePtr(r protocol.Range) *protocol.Range { return &r }
