package lsp

import (
	"context"
	"slices"

	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// onDefinition answers `textDocument/definition` with the declaration the
// identifier at the cursor names.
func (s *server) onDefinition(_ context.Context, params protocol.DefinitionParams) (any, error) {
	r, ok := s.open(params.TextDocument.URI)
	if !ok {
		return []protocol.Location{}, nil
	}
	v := r.project()
	view := r.view()
	c := view.cursorAt(params.Position)
	if c.at < 0 || view.tokens[c.at].Kind != lexer.Ident {
		return []protocol.Location{}, nil
	}
	if loc, ok := r.enumValueDefinition(c); ok {
		return []protocol.Location{loc}, nil
	}
	d := v.symbolAt(view, c.at)
	if d == nil {
		return []protocol.Location{}, nil
	}
	return []protocol.Location{v.locationOf(d.DeclNamePos(), len(d.DeclName()), r.uri)}, nil
}

// enumValueDefinition resolves a value named in a field's `@default(...)` or
// `@example(...)` to its declaration in the field's enum type.
func (r *request) enumValueDefinition(c cursor) (protocol.Location, bool) {
	view := r.view()
	decName, _, ok := decoratorArgContext(view, c)
	if !ok || (decName != "default" && decName != "example") {
		return protocol.Location{}, false
	}
	f := fieldAtCursor(view, c)
	if f == nil || f.Type == nil || f.Type.Named == nil || f.Type.Named.Name == nil || typedByTypeParam(view, f) {
		return protocol.Location{}, false
	}
	v := r.project()
	e, ok := v.lookup(f.Type.Named.Name.String(), semantic.EnumDecls).(*ast.EnumDecl)
	if !ok {
		return protocol.Location{}, false
	}
	for _, val := range e.EnumValues() {
		if val.Name == view.tokens[c.at].Text {
			return v.locationOf(val.Pos, len(val.Name), r.uri), true
		}
	}
	return protocol.Location{}, false
}

// enclosingDecl returns the index of the keyword of the declaration holding
// token idx (-1 at file level) and the brace depth there. The walk is forward:
// a keyword is also a legal field name, and only its depth tells them apart.
func enclosingDecl(view snapshotView, idx int) (int, int) {
	last, depth := -1, 0
	for i := range view.outsideParens(-1) {
		if i >= idx {
			break
		}
		switch k := view.tokens[i].Kind; {
		case k == lexer.LBrace:
			depth++
		case k == lexer.RBrace:
			if depth > 0 {
				depth--
			}
			if depth == 0 {
				last = -1
			}
		case depth == 0 && isDeclKeyword(k):
			last = i
		}
	}
	return last, depth
}

// symbolAt returns the declaration the identifier at token idx of sv names,
// resolved from sv's package as the analyser resolves it; nil when it names
// none.
func (v projectView) symbolAt(sv snapshotView, idx int) ast.Decl {
	if sv.kind(idx) != lexer.Ident {
		return nil
	}
	kinds := lookupKindAt(sv, idx)
	if kinds == 0 {
		return nil
	}
	return v.proj.Lookup(sv.packageName(), qualifiedNameAt(sv, idx), kinds)
}

// lookupKindAt returns the kinds of declaration the identifier at token idx
// names, judged by the tokens around it; 0 when it names none.
func lookupKindAt(view snapshotView, idx int) semantic.DeclKind {
	if decName, _, ok := decoratorArgContext(view, view.cursorOn(idx)); ok {
		switch decName {
		case "middlewares":
			return semantic.MiddlewareDecls
		case "errors":
			return semantic.ErrorDecls
		}
		return 0
	}
	if view.kind(idx-1) == lexer.At {
		return 0 // a decorator's name
	}
	kw, depth := enclosingDecl(view, idx)
	site := declSites[view.kind(kw)]
	switch {
	case kw < 0:
		return 0 // the package clause, an import, or text outside every declaration
	case depth == 0 && idx == headerName(view, kw):
		return site.names
	case depth == 0:
		return 0 // a type parameter, an error's category, a scalar's primitive
	case site.block == blockEnum || isMemberName(view, kw, idx):
		return 0
	}
	return semantic.TypeRefDecls
}

// headerName returns the index of the name the header of the declaration at
// keyword kw declares, or extends for `extend service Name`.
func headerName(view snapshotView, kw int) int {
	switch view.kind(kw) {
	case lexer.KwError, lexer.KwExtend:
		return kw + 2 // `error Category Name`, `extend service Name`
	}
	return kw + 1
}

// isMemberName reports whether identifier idx, in the body of the
// declaration at keyword kw, names no declaration: a field, a method or a
// word of its route, or a type parameter of kw's type.
func isMemberName(view snapshotView, kw, idx int) bool {
	tok := view.tokens[idx]
	if f, _ := findFieldAtPos(view.file, tok.Pos); f != nil {
		return true
	}
	if declSites[view.kind(kw)].block == blockService && (view.kind(idx-1).IsVerb() || inRoute(view, idx)) {
		return true
	}
	return qualifiedNameAt(view, idx) == tok.Text && slices.Contains(typeParamsAt(view, kw), tok.Text)
}

// inRoute reports whether identifier idx is a word of a method's route, such
// as `users` or `id` in `/users/{id}`.
func inRoute(view snapshotView, idx int) bool {
	line := view.tokens[idx].Pos.Line
	for i := idx - 1; i >= 0 && view.tokens[i].Pos.Line == line; i-- {
		switch k := view.kind(i); {
		case k == lexer.Slash:
			return true
		case k == lexer.LBrace && !isRouteParam(view, i):
			return false // the method body's brace
		case k != lexer.LBrace && k != lexer.RBrace && k != lexer.Dash && k != lexer.Ident && !k.IsKeyword():
			return false
		}
	}
	return false
}

// isRouteParam reports whether the `{` at token i opens a route variable:
// `/{word}`, the only shape the parser takes for one.
func isRouteParam(view snapshotView, i int) bool {
	next := view.kind(i + 1)
	return view.kind(i-1) == lexer.Slash && (next == lexer.Ident || next.IsKeyword()) && view.kind(i+2) == lexer.RBrace
}

// typeParamsAt returns the type parameters of the type declared at keyword kw.
func typeParamsAt(view snapshotView, kw int) []string {
	if view.file == nil {
		return nil
	}
	for _, d := range view.file.Decls {
		if td, ok := d.(*ast.TypeDecl); ok && td.Pos == view.tokens[kw].Pos {
			return td.TypeParams
		}
	}
	return nil
}

// qualifiedNameAt returns the identifier at idx, or `pkg.Name` when it is
// either half of a dotted reference.
func qualifiedNameAt(view snapshotView, idx int) string {
	tok := view.tokens[idx]
	switch {
	case tok.Kind != lexer.Ident:
		return tok.Text
	case view.kind(idx-1) == lexer.Dot && view.kind(idx-2) == lexer.Ident:
		return view.tokens[idx-2].Text + "." + tok.Text
	case isQualifier(view, idx):
		return tok.Text + "." + view.tokens[idx+2].Text
	}
	return tok.Text
}

// isQualifier reports whether token idx is the package half of `pkg.Name`.
func isQualifier(view snapshotView, idx int) bool {
	return view.kind(idx+1) == lexer.Dot && view.kind(idx+2) == lexer.Ident
}

// onReferences answers `textDocument/references` with every identifier in the
// project that names the declaration the one at the cursor names.
func (s *server) onReferences(_ context.Context, params protocol.ReferenceParams) (any, error) {
	r, ok := s.open(params.TextDocument.URI)
	if !ok {
		return []protocol.Location{}, nil
	}
	v := r.project()
	view := r.view()
	c := view.cursorAt(params.Position)
	if c.at < 0 {
		return []protocol.Location{}, nil
	}
	d := v.symbolAt(view, c.at)
	if d == nil {
		return []protocol.Location{}, nil
	}
	return v.references(d, params.Context.IncludeDeclaration, r.uri), nil
}

// references returns the location of every identifier in the project that
// names d; d's own name is left out unless includeDecl.
func (v projectView) references(d ast.Decl, includeDecl bool, current protocol.DocumentURI) []protocol.Location {
	out := []protocol.Location{}
	for _, lf := range v.files {
		u := v.uriOf(lf.path, current)
		for i, t := range lf.tokens {
			if !includeDecl && t.Pos == d.DeclNamePos() {
				continue
			}
			if t.Kind == lexer.Ident && t.Text == d.DeclName() && !isQualifier(lf.snapshotView, i) && v.symbolAt(lf.snapshotView, i) == d {
				out = append(out, protocol.Location{URI: u, Range: rangeOf(lf.src, t)})
			}
		}
	}
	return out
}

// onDocumentHighlight answers `textDocument/documentHighlight` with every
// identifier in the buffer spelt like the one at the cursor.
func (s *server) onDocumentHighlight(_ context.Context, params protocol.DocumentHighlightParams) (any, error) {
	r, ok := s.open(params.TextDocument.URI)
	if !ok {
		return []protocol.DocumentHighlight{}, nil
	}
	view := r.view()
	c := view.cursorAt(params.Position)
	if c.at < 0 || view.tokens[c.at].Kind != lexer.Ident {
		return []protocol.DocumentHighlight{}, nil
	}
	name := view.tokens[c.at].Text
	out := []protocol.DocumentHighlight{}
	for _, t := range view.tokens {
		if t.Kind != lexer.Ident || t.Text != name {
			continue
		}
		out = append(out, protocol.DocumentHighlight{Range: rangeOf(view.src, t), Kind: protocol.DocumentHighlightKindText})
	}
	return out, nil
}
