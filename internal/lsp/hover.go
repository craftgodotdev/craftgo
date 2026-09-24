package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/errcat"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// isVerbToken reports whether t is an HTTP verb keyword.
func isVerbToken(t lexer.Token) bool {
	switch t.Kind {
	case lexer.VerbGet, lexer.VerbPost, lexer.VerbPut, lexer.VerbPatch,
		lexer.VerbDelete, lexer.VerbHead, lexer.VerbOptions:
		return true
	}
	return false
}

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
	site, _ := enclosingDeclKeyword(view, idx+1)
	if site == lexer.KwExtend {
		site = lexer.KwService
	}
	if site != kd.site {
		return nil
	}
	return &protocol.Hover{
		Contents: protocol.MarkupContent{Kind: protocol.Markdown, Value: kd.doc},
		Range:    rangePtr(rangeOf(tok)),
	}
}

// onHover answers `textDocument/hover`, with null where there is nothing to show.
func (s *server) onHover(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
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

// hoverForToken returns the hover for token idx from the buffer alone, or nil.
func hoverForToken(view snapshotView, idx int, tok lexer.Token) *protocol.Hover {
	// `@name`: the cursor is on the `@` or on the name.
	if tok.Kind == lexer.At && idx+1 < len(view.tokens) {
		next := view.tokens[idx+1]
		if next.Kind == lexer.Ident && view.tokens[idx+1].Pos.Line == tok.Pos.Line {
			return decoratorHover(next.Text, joinedRange(tok, next))
		}
	}
	if tok.Kind == lexer.Ident && idx > 0 && view.tokens[idx-1].Kind == lexer.At {
		return decoratorHover(tok.Text, joinedRange(view.tokens[idx-1], tok))
	}
	if h := formatRawArgHover(view, idx, tok); h != nil {
		return h
	}
	if doc, ok := verbDocs[tok.Text]; ok && isVerbToken(tok) {
		return &protocol.Hover{
			Contents: protocol.MarkupContent{Kind: protocol.Markdown, Value: doc},
			Range:    rangePtr(rangeOf(tok)),
		}
	}
	if h := memberKeywordHover(view, idx, tok); h != nil {
		return h
	}
	// Any identifier spelt like a documented built-in gets its doc.
	if tok.Kind == lexer.Ident {
		if sp, ok := prims.Lookup(tok.Text); ok && sp.Doc != "" {
			return &protocol.Hover{
				Contents: protocol.MarkupContent{Kind: protocol.Markdown, Value: sp.Doc},
				Range:    rangePtr(rangeOf(tok)),
			}
		}
		if errcat.IsCategory(tok.Text) {
			return errorCategoryHover(tok)
		}
		if d := findDecl(view.file, tok.Text); d != nil {
			return userTypeHover(d, rangeOf(tok))
		}
		// A field's own name token shows the field.
		if f, parent := findFieldAtPos(view.file, tok.Pos); f != nil {
			return fieldHover(parent, f, rangeOf(tok))
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
		Range:    rangePtr(rangeOf(tok)),
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
				return fd, declName(d)
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

// typeRefString prints t as the source spells it: `User[]`, `Page<User>?`,
// `map<string, int>`.
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

// hoverWithProject is [hoverForToken] with a project-wide declaration lookup as
// the fallback, so the project loads only when the buffer has no answer.
func (s *server) hoverWithProject(view snapshotView, idx int, tok lexer.Token, currentURI string, currentSrc string) *protocol.Hover {
	if h := hoverForToken(view, idx, tok); h != nil {
		return h
	}
	if tok.Kind != lexer.Ident {
		return nil
	}
	v := s.loadProject(uriToPath(currentURI), currentSrc)
	if d := v.lookup(qualifiedNameAt(view, idx), semantic.AnyDecl); d != nil {
		return userTypeHover(d, rangeOf(tok))
	}
	return nil
}

// decoratorHover renders `@name` from its registry entry, or says the name is
// removed or unknown.
func decoratorHover(name string, r protocol.Range) *protocol.Hover {
	spec, ok := semantic.Registry[name]
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

// errorCategoryHover renders the hover of an error category name.
func errorCategoryHover(tok lexer.Token) *protocol.Hover {
	body := fmt.Sprintf("**`%s`** - built-in error category.\n\nReserved name; use as `error %s YourErrorName` to declare an error of this kind.", tok.Text, tok.Text)
	return &protocol.Hover{
		Contents: protocol.MarkupContent{Kind: protocol.Markdown, Value: body},
		Range:    rangePtr(rangeOf(tok)),
	}
}

// joinedRange returns the range from a's start to b's end; both must be on
// one line.
func joinedRange(a, b lexer.Token) protocol.Range {
	end := b.Pos
	end.Column += len(b.Text)
	return protocol.Range{Start: lspPos(a.Pos), End: lspPos(end)}
}

func rangePtr(r protocol.Range) *protocol.Range { return &r }
