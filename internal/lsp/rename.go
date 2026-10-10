package lsp

import (
	"context"
	"fmt"
	"unicode"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// onPrepareRename answers `textDocument/prepareRename` with the range of an
// identifier spelt like the declaration it names, else null.
func (s *server) onPrepareRename(_ context.Context, params protocol.PrepareRenameParams) (any, error) {
	r, ok := s.open(params.TextDocument.URI)
	if !ok {
		return nil, nil
	}
	view := r.view()
	c := view.cursorAt(params.Position)
	if r.renameTarget(c) == nil {
		return nil, nil
	}
	rng := rangeOf(view.src, view.tokens[c.at])
	return &rng, nil
}

// onRename answers `textDocument/rename`, rewriting every identifier in the
// project that names the declaration the one at the cursor names. A new name
// the declaration cannot take is an InvalidParams error.
func (s *server) onRename(_ context.Context, params protocol.RenameParams) (any, error) {
	if !lexer.IsIdent(params.NewName) {
		return nil, jsonrpc2.NewError(jsonrpc2.InvalidParams, fmt.Sprintf("invalid rename target %q: not a craftgo identifier", params.NewName))
	}
	r, ok := s.open(params.TextDocument.URI)
	if !ok {
		return nil, nil
	}
	d := r.renameTarget(r.view().cursorAt(params.Position))
	if d == nil {
		return nil, nil
	}
	if why := r.project().renameFault(d, params.NewName); why != "" {
		return nil, jsonrpc2.NewError(jsonrpc2.InvalidParams, fmt.Sprintf("cannot rename %s to %q: %s", d.DeclName(), params.NewName, why))
	}
	changes := map[protocol.DocumentURI][]protocol.TextEdit{}
	for _, loc := range r.project().references(d, true, r.uri) {
		changes[loc.URI] = append(changes[loc.URI], protocol.TextEdit{
			Range:   loc.Range,
			NewText: params.NewName,
		})
	}
	if len(changes) == 0 {
		// The buffer is always listed: the rename UI fails on an empty map.
		changes[r.uri] = []protocol.TextEdit{}
	}
	return &protocol.WorkspaceEdit{Changes: changes}, nil
}

// renameFault says why d cannot take the name name, or returns "": a
// built-in's name, a lower-case one for a declaration whose name the analyser
// wants upper-case, or one another declaration holds where d's would clash
// with it.
func (v projectView) renameFault(d ast.Decl, name string) string {
	if name == d.DeclName() {
		return ""
	}
	if prims.Is(name) {
		return "it names a built-in type"
	}
	_, service := d.(*ast.ServiceDecl)
	if !service && !unicode.IsUpper([]rune(name)[0]) {
		return "a declaration's name starts with an upper-case letter"
	}
	for pkgName, pkg := range v.proj.Packages {
		other := pkg.Decl(name, semantic.AnyDecl)
		if other == nil {
			continue
		}
		_, global := d.(*ast.MiddlewareDecl)
		if pkgName == v.packageOf(d) || global || service {
			return fmt.Sprintf("%s already declares it", infoOf(other).summary)
		}
	}
	return ""
}

// packageOf returns the name of the package declaring d.
func (v projectView) packageOf(d ast.Decl) string {
	for name, pkg := range v.proj.Packages {
		for _, other := range pkg.Decls(semantic.AnyDecl) {
			if other == d {
				return name
			}
		}
	}
	return ""
}

// renameTarget returns the declaration the identifier at c names, or nil on
// the package half of `pkg.Name`.
func (r *request) renameTarget(c cursor) ast.Decl {
	if c.at < 0 || isQualifier(r.view(), c.at) {
		return nil
	}
	return r.project().symbolAt(r.view(), c.at)
}
