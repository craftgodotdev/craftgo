package lsp

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/parser"
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

// An error named like a category hovers as the error; the category slot of
// its header hovers as the category.
func TestHoverOnAnErrorNamedLikeACategory(t *testing.T) {
	const src = "package x\n\nerror NotFound Gone\n\nservice S {\n\t@errors(Gone)\n\tget G /g {}\n}\n"
	for _, at := range []string{"NotFound Go|ne", "@errors(Go|ne)"} {
		marked := strings.Replace(src, strings.Replace(at, "|", "", 1), at, 1)
		if h := hoverAt(t, "", marked); !strings.Contains(h, "error NotFound Gone") {
			t.Errorf("hover at %q = %q, want the error", at, h)
		}
	}
	if h := hoverAt(t, "", strings.Replace(src, "NotFound Gone", "Not|Found Gone", 1)); !strings.Contains(h, "built-in error category") {
		t.Errorf("hover on the category = %q", h)
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

// namesDSL spaces each declared name away from its keyword.
const namesDSL = "package x\n\n@doc(\"m\")\nmiddleware   AuthRequired\n\ntype  User { name string }\n\n" +
	"error NotFound   Gone\n\nservice S {\n\t@middlewares(AuthRequired)\n\tget   Fetch /f { response User }\n}\n\n" +
	"event  Placed { payload User }\n"

// Definition, the outline, workspace symbols and references place a
// declaration at its name.
func TestDeclarationRangesCoverTheName(t *testing.T) {
	u := uri.New("file:///t.craftgo")
	s := &server{docs: map[uri.URI]string{u: namesDSL}}
	marked := strings.Replace(namesDSL, "@middlewares(AuthRequired)", "@middlewares(Auth|Required)", 1)
	for _, l := range definitionAt(t, "", marked) {
		if got := rangeText(namesDSL, l.Range); got != "AuthRequired" {
			t.Errorf("definition covers %q", got)
		}
	}
	res, _ := callHandler(t, s, protocol.MethodTextDocumentDocumentSymbol, protocol.DocumentSymbolParams{TextDocument: protocol.TextDocumentIdentifier{URI: u}})
	var walk func(syms []protocol.DocumentSymbol)
	walk = func(syms []protocol.DocumentSymbol) {
		for _, sym := range syms {
			if got := rangeText(namesDSL, sym.SelectionRange); got != sym.Name {
				t.Errorf("outline selects %q for %s", got, sym.Name)
			}
			if !strings.Contains(rangeText(namesDSL, sym.Range), sym.Name) {
				t.Errorf("outline range of %s covers %q", sym.Name, rangeText(namesDSL, sym.Range))
			}
			walk(sym.Children)
		}
	}
	walk(res.([]protocol.DocumentSymbol))
	res, _ = callHandler(t, s, protocol.MethodWorkspaceSymbol, protocol.WorkspaceSymbolParams{})
	for _, sym := range res.([]protocol.SymbolInformation) {
		if got := rangeText(namesDSL, sym.Location.Range); got != sym.Name {
			t.Errorf("workspace symbol %s covers %q", sym.Name, got)
		}
	}
	_, pos := markCursor(t, strings.Replace(namesDSL, "type  User", "type  Us|er", 1))
	res, _ = callHandler(t, s, protocol.MethodTextDocumentReferences, protocol.ReferenceParams{TextDocumentPositionParams: docAt(u, pos)})
	for _, l := range res.([]protocol.Location) {
		if l.Range.Start.Line == 5 {
			t.Errorf("references without the declaration list it: %+v", l)
		}
	}
	if n := len(res.([]protocol.Location)); n != 2 {
		t.Errorf("references = %d, want the response and the payload", n)
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

// mustParseFile parses src, failing on any parse diagnostic.
func mustParseFile(t *testing.T, path, src string) *ast.File {
	t.Helper()
	p := parser.New(path, src)
	f := p.Parse()
	if pd := p.Diagnostics(); len(pd) > 0 {
		t.Fatalf("parse %s: %v", path, pd)
	}
	return f
}

// A type position names a type, an enum or a scalar; an error's own name
// still resolves to the error.
func TestDefinitionTypePositionNamesTypesOnly(t *testing.T) {
	const design = "package x\n\nerror NotFound Gone\nservice Svc { get G /g {} }\ntype H {\n\ta Gone\n\tb Svc\n}\n"
	for _, field := range []string{"a Gone", "b Svc"} {
		marked := strings.Replace(design, field, field[:3]+cursorMark+field[3:], 1)
		if locs := definitionAt(t, "", marked); len(locs) != 0 {
			t.Errorf("`%s` is no type reference, yet definition answers %+v", field, locs)
		}
	}
	locs := definitionAt(t, "", strings.Replace(design, "NotFound Gone", "NotFound Go"+cursorMark+"ne", 1))
	if len(locs) != 1 || locs[0].Range.Start.Line != 2 {
		t.Errorf("the error's own name resolves to %+v, want the error on line 3", locs)
	}
}

// Inside `@middlewares(...)` a name resolves to the middleware, not to a
// same-named error.
func TestDefinitionPrefersKindFromDecoratorContext(t *testing.T) {
	src := `package x
error Forbidden AuthRequired { reason string }
middleware AuthRequired
service S {
	@middlewares(AuthRequired)
	get GetThing /things/{id} {}
}
`
	view := parseSnapshot("t.craftgo", src)
	// The third AuthRequired is the one inside @middlewares(...).
	var inside protocol.Position
	count := 0
	for _, tok := range view.tokens {
		if tok.Text != "AuthRequired" {
			continue
		}
		count++
		if count == 3 {
			inside = protocol.Position{Line: uint32(tok.Pos.Line - 1), Character: uint32(tok.Pos.Column - 1)}
			break
		}
	}
	if count < 3 {
		t.Fatalf("expected at least 3 AuthRequired tokens, got %d", count)
	}
	decName, _, ok := decoratorArgContext(view, view.cursorAt(inside))
	if !ok || decName != "middlewares" {
		t.Fatalf("decoratorArgContext should detect @middlewares, got name=%q ok=%v", decName, ok)
	}
	d := lookupIn(t, "x", "AuthRequired", semantic.MiddlewareDecls, view.file)
	if d == nil {
		t.Fatal("middleware lookup returned nil")
	}
	if _, isMW := d.(*ast.MiddlewareDecl); !isMW {
		t.Errorf("expected MiddlewareDecl, got %T", d)
	}
}

// A type position never resolves to a same-named middleware.
func TestDefinitionTypePositionExcludesMiddleware(t *testing.T) {
	src := `package x
middleware Greeter
type Greeter { id string }
type Holder { g Greeter }
`
	view := parseSnapshot("t.craftgo", src)
	// The third Greeter is the field type in `g Greeter`.
	count := 0
	var fieldTypePos protocol.Position
	for _, tok := range view.tokens {
		if tok.Text != "Greeter" {
			continue
		}
		count++
		if count == 3 {
			fieldTypePos = protocol.Position{Line: uint32(tok.Pos.Line - 1), Character: uint32(tok.Pos.Column - 1)}
			break
		}
	}
	if kind := lookupKindAt(view, view.cursorAt(fieldTypePos).at); kind != semantic.TypeRefDecls {
		t.Errorf("expected the type-reference kinds for a field-type position, got %v", kind)
	}
	d := lookupIn(t, "x", "Greeter", semantic.TypeRefDecls, view.file)
	if d == nil {
		t.Fatal("type-context lookup returned nil")
	}
	if _, isMW := d.(*ast.MiddlewareDecl); isMW {
		t.Errorf("type-context lookup wrongly returned MiddlewareDecl")
	}
	if _, isType := d.(*ast.TypeDecl); !isType {
		t.Errorf("expected TypeDecl, got %T", d)
	}
}

// Across files a name resolves to the middleware or the error the lookup
// kinds select.
func TestDefinitionKindAwareAcrossFiles(t *testing.T) {
	mwFile := `package shared
middleware AuthRequired
`
	errFile := `package shared
error Forbidden AuthRequired { reason string }
`
	useFile := `package services
import "shared"
service S {
	@middlewares(AuthRequired)
	get GetX /x {}
}
`
	files := []*ast.File{
		mustParseFile(t, "shared/mw.craftgo", mwFile),
		mustParseFile(t, "shared/err.craftgo", errFile),
		mustParseFile(t, "services/use.craftgo", useFile),
	}
	d := lookupIn(t, "services", "AuthRequired", semantic.MiddlewareDecls, files...)
	if d == nil {
		t.Fatal("middleware lookup did not find the decl across files")
	}
	if _, isMW := d.(*ast.MiddlewareDecl); !isMW {
		t.Errorf("expected MiddlewareDecl across files, got %T from %s", d, d.DeclPos().Filename)
	}
	if got := d.DeclPos().Filename; got != "shared/mw.craftgo" {
		t.Errorf("expected hit in shared/mw.craftgo, got %s", got)
	}
	d = lookupIn(t, "services", "AuthRequired", semantic.ErrorDecls, files...)
	if d == nil {
		t.Fatal("error lookup did not find the decl across files")
	}
	if _, isErr := d.(*ast.ErrorDecl); !isErr {
		t.Errorf("expected ErrorDecl, got %T", d)
	}
	if got := d.DeclPos().Filename; got != "shared/err.craftgo" {
		t.Errorf("expected hit in shared/err.craftgo, got %s", got)
	}
}

// The name in `extend service X` resolves to X's primary block.
func TestDefinitionExtendServiceJumpsToPrimary(t *testing.T) {
	src := `package x
service Alpha {
	get A /a {}
}

extend service Alpha {
	get B /b {}
}
`
	view := parseSnapshot("t.craftgo", src)
	// The second Alpha is the name in the extend header.
	count := 0
	var pos protocol.Position
	for _, tok := range view.tokens {
		if tok.Text != "Alpha" {
			continue
		}
		count++
		if count == 2 {
			pos = protocol.Position{Line: uint32(tok.Pos.Line - 1), Character: uint32(tok.Pos.Column - 1)}
			break
		}
	}
	if count < 2 {
		t.Fatalf("expected 2 Alpha tokens, got %d", count)
	}
	if kind := lookupKindAt(view, view.cursorAt(pos).at); kind != semantic.ServiceDecls {
		t.Fatalf("expected service kinds for an extend header, got %v", kind)
	}
	d := lookupIn(t, "x", "Alpha", semantic.ServiceDecls, view.file)
	sd, ok := d.(*ast.ServiceDecl)
	if !ok {
		t.Fatalf("expected ServiceDecl, got %T", d)
	}
	if sd.Extend {
		t.Error("resolved to the extend block; the primary is the definition site")
	}
	if sd.Pos.Line != 2 {
		t.Errorf("expected the primary at line 2, got %d", sd.Pos.Line)
	}
}

// An extend in a file of its own resolves to the primary in a sibling file.
func TestDefinitionExtendServiceResolvesAcrossFiles(t *testing.T) {
	primary := `package x
service Alpha {
	get A /a {}
}
`
	ext := `package x

extend service Alpha {
	get B /b {}
}
`
	view := parseSnapshot("alpha-extra.craftgo", ext)
	pos := protocol.Position{Line: 2, Character: 15} // the Alpha in the extend header
	_, tok := tokenUnder(view, pos)
	if tok.Text != "Alpha" {
		t.Fatalf("probe landed on %q, not the service name", tok.Text)
	}
	if kind := lookupKindAt(view, view.cursorAt(pos).at); kind != semantic.ServiceDecls {
		t.Fatalf("expected service kinds, got %v", kind)
	}
	// The extend-only file holds no definition site.
	if d := lookupIn(t, "x", "Alpha", semantic.ServiceDecls, view.file); d != nil {
		t.Errorf("extend-only file has no definition site, got %T", d)
	}
	d := lookupIn(t, "x", "Alpha", semantic.ServiceDecls,
		mustParseFile(t, "x/alpha-extra.craftgo", ext),
		mustParseFile(t, "x/alpha.craftgo", primary))
	if d == nil {
		t.Fatal("project-wide lookup did not find the primary service")
	}
	sd, isSvc := d.(*ast.ServiceDecl)
	if !isSvc || sd.Extend {
		t.Fatalf("expected the primary ServiceDecl, got %T (extend=%v)", d, isSvc && sd.Extend)
	}
	if got := d.DeclPos().Filename; got != "x/alpha.craftgo" {
		t.Errorf("expected the hit in x/alpha.craftgo, got %s", got)
	}
}

// A service header's name names a service, even after a type declaration.
func TestDefinitionServiceHeaderNamesAService(t *testing.T) {
	src := `package x
type Thing { id string }

extend service Alpha {
	get B /b {}
}
`
	view := parseSnapshot("t.craftgo", src)
	pos := protocol.Position{Line: 3, Character: 15}
	idx, tok := tokenUnder(view, pos)
	if tok.Text != "Alpha" {
		t.Fatalf("probe landed on %q, not the service name", tok.Text)
	}
	if kind := lookupKindAt(view, idx); kind != semantic.ServiceDecls {
		t.Errorf("a service header's name resolves among %v, want the services", kind)
	}
}

// `textDocument/definition` on an extend's name replies with the primary's
// location in its own file.
func TestDefinitionExtendServiceEndToEnd(t *testing.T) {
	root := t.TempDir()
	design := filepath.Join(root, "design")
	if err := os.MkdirAll(design, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "craftgo.design.yaml"), []byte("package: example.com/p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	primary := "package x\nservice Alpha {\n\tget A /a {}\n}\n"
	if err := os.WriteFile(filepath.Join(design, "alpha.craftgo"), []byte(primary), 0o644); err != nil {
		t.Fatal(err)
	}
	ext := "package x\n\nextend service Alpha {\n\tget B /b {}\n}\n"
	extPath := filepath.Join(design, "alpha-extra.craftgo")
	if err := os.WriteFile(extPath, []byte(ext), 0o644); err != nil {
		t.Fatal(err)
	}

	extURI := uri.File(extPath)
	srv := &server{docs: map[uri.URI]string{extURI: ext}}
	// `extend service Alpha` - the name starts at column 15.
	res, err := callHandler(t, srv, protocol.MethodTextDocumentDefinition, protocol.DefinitionParams{
		TextDocumentPositionParams: docAt(extURI, protocol.Position{Line: 2, Character: 15}),
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	got, ok := res.([]protocol.Location)
	if !ok {
		t.Fatalf("unexpected reply type %T", res)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 location, got %d (%v)", len(got), got)
	}
	if filepath.Base(uriToPath(string(got[0].URI))) != "alpha.craftgo" {
		t.Errorf("jumped into %s, want the primary's file alpha.craftgo", uriToPath(string(got[0].URI)))
	}
	if got[0].Range.Start.Line != 1 {
		t.Errorf("want the primary declaration on line 2 (0-indexed 1), got %d", got[0].Range.Start.Line)
	}
}

// Inside `@errors(...)` a name resolves to the error, not to a same-named
// middleware.
func TestDefinitionErrorContextResolvesToError(t *testing.T) {
	src := `package x
middleware Conflict
error Conflict Conflict { reason string }
service S {
	@errors(Conflict)
	get GetThing /t {}
}
`
	view := parseSnapshot("t.craftgo", src)
	d := lookupIn(t, "x", "Conflict", semantic.ErrorDecls, view.file)
	if d == nil {
		t.Fatal("error lookup returned nil")
	}
	if _, isErr := d.(*ast.ErrorDecl); !isErr {
		t.Errorf("expected ErrorDecl, got %T", d)
	}
}

// lookupIn analyses files as one project and resolves name from package
// homePkg among the selected declaration kinds.
func lookupIn(t *testing.T, homePkg, name string, kinds semantic.DeclKind, files ...*ast.File) ast.Decl {
	t.Helper()
	proj, _ := semantic.AnalyzeProject(files, semantic.Options{})
	return proj.Lookup(homePkg, name, kinds)
}
