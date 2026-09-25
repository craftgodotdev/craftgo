package lsp

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// openMarked writes marked without its cursor mark to path and returns a
// server holding it open, its URI and the mark's position; an empty path
// opens it outside any project.
func openMarked(t *testing.T, path, marked string) (*server, uri.URI, protocol.Position) {
	t.Helper()
	src, pos := markCursor(t, marked)
	u := uri.New("file:///t.craftgo")
	if path != "" {
		mustWrite(t, path, src)
		u = uri.File(path)
	}
	return &server{docs: map[uri.URI]string{u: src}}, u, pos
}

// definitionAt answers `textDocument/definition` at the cursor mark of
// marked, as [openMarked] opens it.
func definitionAt(t *testing.T, path, marked string) []protocol.Location {
	t.Helper()
	s, u, pos := openMarked(t, path, marked)
	res, err := callHandler(t, s, protocol.MethodTextDocumentDefinition, protocol.DefinitionParams{TextDocumentPositionParams: docAt(u, pos)})
	if err != nil {
		t.Fatal(err)
	}
	return res.([]protocol.Location)
}

// hoverAt returns the hover text at the cursor mark of marked, as
// [openMarked] opens it; "" for no hover.
func hoverAt(t *testing.T, path, marked string) string {
	t.Helper()
	s, u, pos := openMarked(t, path, marked)
	if h := hoverReply(t, s, u, pos); h != nil {
		return h.Contents.Value
	}
	return ""
}

// hoverReply answers `textDocument/hover` at pos of the open document u.
func hoverReply(t *testing.T, s *server, u uri.URI, pos protocol.Position) *protocol.Hover {
	t.Helper()
	res, err := callHandler(t, s, protocol.MethodTextDocumentHover, protocol.HoverParams{TextDocumentPositionParams: docAt(u, pos)})
	if err != nil {
		t.Fatal(err)
	}
	h, _ := res.(*protocol.Hover)
	return h
}

// A bare name resolves as the analyser resolves it: a type in its own
// package only, a middleware in any package.
func TestDefinitionOfABareNameFollowsTheAnalyser(t *testing.T) {
	design := designProject(t, map[string]string{
		"b/b.craftgo": "package b\n\ntype Foo { y int }\nmiddleware Auth\n",
	})
	path := filepath.Join(design, "a", "a.craftgo")
	if locs := definitionAt(t, path, "package a\n\ntype H { x Fo|o }\n"); len(locs) != 0 {
		t.Errorf("bare Foo is unknown in package a, yet definition answers %+v", locs)
	}
	locs := definitionAt(t, path, "package a\n\nservice S {\n\t@middlewares(Au|th)\n\tget G /g {}\n}\n")
	if len(locs) != 1 || filepath.Base(uriToPath(string(locs[0].URI))) != "b.craftgo" {
		t.Errorf("bare Auth resolves to %+v, want the middleware of package b", locs)
	}
}

// Hover shows the declaration definition jumps to: inside `@middlewares(X)`
// the middleware, though a type X exists too.
func TestHoverShowsWhatDefinitionFinds(t *testing.T) {
	marked := "package x\n\ntype AuthRequired { a string }\nmiddleware AuthRequired\n\nservice S {\n\t@middlewares(Auth|Required)\n\tget G /g {}\n}\n"
	if h := hoverAt(t, "", marked); !strings.Contains(h, "middleware AuthRequired") {
		t.Errorf("hover = %q, want the middleware", h)
	}
	if locs := definitionAt(t, "", marked); len(locs) != 1 || locs[0].Range.Start.Line != 3 {
		t.Errorf("definition = %+v, want the middleware on line 4", locs)
	}
}

// Hover on `b.Name` shows package b's declaration, not the buffer's own Name.
func TestHoverOnAQualifiedName(t *testing.T) {
	design := designProject(t, map[string]string{
		"b/b.craftgo": "package b\n\n// remote one\ntype Name { y int }\n",
	})
	h := hoverAt(t, filepath.Join(design, "a", "a.craftgo"),
		"package a\n\nimport \"b\"\n\n// local one\ntype Name { x string }\n\ntype Holder { n b.Na|me }\n")
	if !strings.Contains(h, "remote one") {
		t.Errorf("hover = %q, want package b's Name", h)
	}
}

// usersProject declares `User` in packages a and b; a's file, returned with
// its path, also has a method and a route word spelt User.
func usersProject(t *testing.T) (string, string) {
	t.Helper()
	design := designProject(t, map[string]string{
		"b/b.craftgo": "package b\n\ntype User { id string }\n",
	})
	src := "package a\n\ntype User { name string }\n\nservice S {\n\tget User /User {\n\t\tresponse User\n\t}\n}\n"
	return filepath.Join(design, "a", "a.craftgo"), src
}

// References and rename reach each identifier that names the declaration: not
// a method or a route word spelt like it, nor another package's declaration.
func TestReferencesAndRenameFollowTheResolver(t *testing.T) {
	path, src := usersProject(t)
	want := []protocol.Position{{Line: 2, Character: 5}, {Line: 6, Character: 11}}
	starts := func(locs []protocol.Location) []protocol.Position {
		var out []protocol.Position
		for _, l := range locs {
			if uriToPath(string(l.URI)) != path {
				t.Errorf("a reference in %s", l.URI)
			}
			out = append(out, l.Range.Start)
		}
		return out
	}
	for _, at := range []string{"type Us|er", "response Us|er"} {
		s, u, pos := openMarked(t, path, strings.Replace(src, strings.Replace(at, "|", "", 1), at, 1))
		res, err := callHandler(t, s, protocol.MethodTextDocumentReferences, protocol.ReferenceParams{
			TextDocumentPositionParams: docAt(u, pos),
			Context:                    protocol.ReferenceContext{IncludeDeclaration: true},
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := starts(res.([]protocol.Location)); !slices.Equal(got, want) {
			t.Errorf("references from %q = %v, want %v", at, got, want)
		}
		res, err = callHandler(t, s, protocol.MethodTextDocumentRename, protocol.RenameParams{TextDocumentPositionParams: docAt(u, pos), NewName: "Member"})
		if err != nil {
			t.Fatal(err)
		}
		edit, _ := res.(*protocol.WorkspaceEdit)
		if edit == nil || len(edit.Changes) != 1 {
			t.Fatalf("rename from %q = %+v, want edits in one file", at, res)
		}
		var edits []protocol.Location
		for eu, es := range edit.Changes {
			for _, e := range es {
				edits = append(edits, protocol.Location{URI: eu, Range: e.Range})
			}
		}
		if got := starts(edits); !slices.Equal(got, want) {
			t.Errorf("rename from %q edits %v, want %v", at, got, want)
		}
	}
}

// On a one-line method at the root path the clause names its type; a word
// between braces there is a route variable only as `/{word}`.
func TestClauseOfARootPathMethod(t *testing.T) {
	const src = "package a\n\ntype ItemList { n int }\n\nservice S {\n\tget ListItems / { response ItemList }\n}\n"
	marked := strings.Replace(src, "response ItemList", "response Item|List", 1)
	if locs := definitionAt(t, "", marked); len(locs) != 1 || locs[0].Range.Start.Line != 2 {
		t.Errorf("definition = %+v, want ItemList on line 3", locs)
	}
}

// An argument list left open above does not swallow the names below it.
func TestAnUnclosedArgumentListLeavesLaterNamesAlone(t *testing.T) {
	const src = "package a\n\ntype Addr { city string }\n\ntype User {\n\tname string @length(1,\n\thome Addr\n}\n\ntype Other {\n\twhere Addr\n}\n"
	marked := strings.Replace(src, "where Addr", "where Ad|dr", 1)
	if locs := definitionAt(t, "", marked); len(locs) != 1 || locs[0].Range.Start.Line != 2 {
		t.Errorf("definition = %+v, want Addr on line 3", locs)
	}
}

// An argument list left open ends with its line: a name on that line is an
// argument, and the declarations below keep their shape.
func TestAnArgumentListLeftOpenEndsWithItsLine(t *testing.T) {
	const mw = "package a\n\nmiddleware Auth\ntype Auth { a string }\n\nservice S {\n\t@middlewares(Auth,\n\tget G /g {}\n}\n"
	if locs := definitionAt(t, "", strings.Replace(mw, "@middlewares(Auth,", "@middlewares(Au|th,", 1)); len(locs) != 1 || locs[0].Range.Start.Line != 2 {
		t.Errorf("definition in an open @middlewares( = %+v, want the middleware on line 3", locs)
	}
	const src = "package a\n\ntype User { name string @length(1,\n}\n\nservice S {\n\tget User /User { response User }\n}\n"
	s, u, pos := openMarked(t, "", strings.Replace(src, "type User", "type Us|er", 1))
	res, err := callHandler(t, s, protocol.MethodTextDocumentReferences, protocol.ReferenceParams{
		TextDocumentPositionParams: docAt(u, pos),
		Context:                    protocol.ReferenceContext{IncludeDeclaration: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if locs := res.([]protocol.Location); len(locs) != 2 {
		t.Errorf("references = %+v, want the declaration and the response", locs)
	}
}

// In `User.User`, where package User declares User, references and rename
// take the name half only.
func TestAPackageSpeltLikeItsDeclaration(t *testing.T) {
	design := designProject(t, map[string]string{"User/u.craftgo": "package User\n\ntype User { id string }\n"})
	path := filepath.Join(design, "b", "b.craftgo")
	s, u, pos := openMarked(t, path, "package b\n\ntype H { u |User.User }\n")
	res, err := callHandler(t, s, protocol.MethodTextDocumentPrepareRename, protocol.PrepareRenameParams{TextDocumentPositionParams: docAt(u, pos)})
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := res.(*protocol.Range); r != nil {
		t.Errorf("prepareRename on the package half = %+v, want none", r)
	}
	pos.Character += uint32(len("User."))
	res, err = callHandler(t, s, protocol.MethodTextDocumentReferences, protocol.ReferenceParams{
		TextDocumentPositionParams: docAt(u, pos),
		Context:                    protocol.ReferenceContext{IncludeDeclaration: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if locs := res.([]protocol.Location); len(locs) != 2 {
		t.Errorf("references = %+v, want the declaration and the name half", locs)
	}
}

// Rename refuses the package half of `pkg.Name`, which names no declaration
// of its own.
func TestRenameRefusesAPackageQualifier(t *testing.T) {
	design := designProject(t, map[string]string{
		"b/b.craftgo": "package b\n\ntype Name { y int }\n",
	})
	s, u, pos := openMarked(t, filepath.Join(design, "a", "a.craftgo"), "package a\n\nimport \"b\"\n\ntype Holder { n |b.Name }\n")
	res, err := callHandler(t, s, protocol.MethodTextDocumentPrepareRename, protocol.PrepareRenameParams{TextDocumentPositionParams: docAt(u, pos)})
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := res.(*protocol.Range); r != nil {
		t.Errorf("prepareRename on a package qualifier = %+v, want none", r)
	}
}

// Each identifier resolves among the kinds of declaration its position
// names, and a name that is no reference among none.
func TestLookupKindAtEveryPosition(t *testing.T) {
	view := parseSnapshot("t.craftgo", `package shop

import lib "lib"

@doc("d")
type Page<T> {
	items T[]
	next Cursor?
	lib.Base
}
enum Kind { Active }
error NotFound Gone { at Kind }
scalar Cursor string
middleware Auth
event Placed { payload Page<Kind> }
service Shop {
	@middlewares(Auth)
	@errors(Gone)
	get Fetch /items/{id} {
		request Page<Kind>
		response Kind
	}
}
extend service Shop {
	post Put /x {}
}
`)
	for _, c := range []struct {
		text string
		nth  int
		want semantic.DeclKind
	}{
		{"shop", 1, 0}, {"lib", 1, 0}, {"doc", 1, 0},
		{"Page", 1, semantic.TypeDecls}, {"T", 1, 0}, {"items", 1, 0}, {"T", 2, 0}, {"next", 1, 0},
		{"Cursor", 1, semantic.TypeRefDecls}, {"lib", 2, semantic.TypeRefDecls}, {"Base", 1, semantic.TypeRefDecls},
		{"Kind", 1, semantic.EnumDecls}, {"Active", 1, 0},
		{"NotFound", 1, 0}, {"Gone", 1, semantic.ErrorDecls}, {"at", 1, 0}, {"Kind", 2, semantic.TypeRefDecls},
		{"Cursor", 2, semantic.ScalarDecls}, {"string", 1, 0},
		{"Auth", 1, semantic.MiddlewareDecls},
		{"Placed", 1, semantic.EventDecls}, {"Page", 2, semantic.TypeRefDecls}, {"Kind", 3, semantic.TypeRefDecls},
		{"Shop", 1, semantic.ServiceDecls}, {"middlewares", 1, 0}, {"Auth", 2, semantic.MiddlewareDecls},
		{"Gone", 2, semantic.ErrorDecls}, {"Fetch", 1, 0}, {"items", 2, 0}, {"id", 1, 0},
		{"Page", 3, semantic.TypeRefDecls}, {"Kind", 4, semantic.TypeRefDecls}, {"Kind", 5, semantic.TypeRefDecls},
		{"Shop", 2, semantic.ServiceDecls}, {"Put", 1, 0}, {"x", 1, 0},
	} {
		if got := lookupKindAt(view, nthToken(t, view, c.text, c.nth)); got != c.want {
			t.Errorf("%s #%d resolves among %v, want %v", c.text, c.nth, got, c.want)
		}
	}
}

// nthToken returns the index of the nth token spelt text.
func nthToken(t *testing.T, view snapshotView, text string, nth int) int {
	t.Helper()
	for i, tok := range view.tokens {
		if tok.Text != text {
			continue
		}
		if nth--; nth == 0 {
			return i
		}
	}
	t.Fatalf("no token %q", text)
	return -1
}
