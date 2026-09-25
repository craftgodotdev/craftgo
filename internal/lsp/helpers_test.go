package lsp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/craftgodotdev/craftgo/internal/lexer"
)

const testDSL = `package design

// Greeter is a sample type used by the LSP test fixtures.
type Greeter {
	id   string @doc("user id") @length(1, 80)
	name string
}

enum Status {
	Active   = "active"
	Inactive = "inactive"
}

@prefix("/v1")
service GreeterService {
	@doc("Hello world.")
	get GetGreeter /{id} {
		request  Greeter
		response Greeter
	}
}
`

// cursorMark marks the cursor in a completion fixture; the DSL has no `|` token.
const cursorMark = "|"

// mustCompletionsAtCursor runs completion at the fixture's cursor mark.
func mustCompletionsAtCursor(t *testing.T, path, src string) []protocol.CompletionItem {
	t.Helper()
	i := strings.Index(src, cursorMark)
	if i < 0 {
		t.Fatalf("fixture carries no %q cursor mark", cursorMark)
	}
	head := src[:i]
	return mustCompletionsAt(t, path, strings.Replace(src, cursorMark, "", 1),
		uint32(strings.Count(head, "\n")),
		uint32(len(head)-(strings.LastIndex(head, "\n")+1)))
}

// designProject writes a manifest and files, keyed by their path under the
// design folder, and returns the design folder.
func designProject(t *testing.T, files map[string]string) string {
	t.Helper()
	design := filepath.Join(t.TempDir(), "design")
	mustWrite(t, filepath.Join(design, "craftgo.design.yaml"), layoutOnly)
	for rel, src := range files {
		mustWrite(t, filepath.Join(design, filepath.FromSlash(rel)), src)
	}
	return design
}

// callHandler sends method with params through the dispatcher and returns the
// reply's result and error.
func callHandler(t *testing.T, s *server, method string, params any) (any, error) {
	t.Helper()
	req, err := jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), method, params)
	if err != nil {
		t.Fatal(err)
	}
	var result any
	var replyErr error
	replier := func(_ context.Context, r any, err error) error {
		result, replyErr = r, err
		return nil
	}
	if err := s.handler(context.Background(), replier, req); err != nil {
		t.Fatal(err)
	}
	return result, replyErr
}

// findToken returns the 0-based LSP position of the first token spelt needle.
func findToken(t *testing.T, view snapshotView, needle string) protocol.Position {
	t.Helper()
	for _, tok := range view.tokens {
		if tok.Text == needle {
			return protocol.Position{Line: uint32(tok.Pos.Line - 1), Character: uint32(tok.Pos.Column - 1)}
		}
	}
	t.Fatalf("token %q not found in fixture", needle)
	return protocol.Position{}
}

// mustHoverAt returns the hover text of the first token spelt needle in src,
// open at a URI built from path.
func mustHoverAt(t *testing.T, path, src, needle string) string {
	t.Helper()
	u := uri.New("file:///" + path)
	pos := findToken(t, parseSnapshot(path, src), needle)
	h := hoverReply(t, &server{docs: map[uri.URI]string{u: src}}, u, pos)
	if h == nil {
		t.Fatalf("expected hover at %q", needle)
	}
	return h.Contents.Value
}

// mustCompletionsAt runs completion at (line, ch) of src, open at a URI built
// from path.
func mustCompletionsAt(t *testing.T, path, src string, line, ch uint32) []protocol.CompletionItem {
	t.Helper()
	u := uri.New("file:///" + path)
	return completionItems(t, &server{docs: map[uri.URI]string{u: src}}, u, protocol.Position{Line: line, Character: ch})
}

// completionItems runs completion at pos in the open document u.
func completionItems(t *testing.T, s *server, u uri.URI, pos protocol.Position) []protocol.CompletionItem {
	t.Helper()
	res, err := callHandler(t, s, protocol.MethodTextDocumentCompletion, protocol.CompletionParams{TextDocumentPositionParams: docAt(u, pos)})
	if err != nil {
		t.Fatal(err)
	}
	return res.(*protocol.CompletionList).Items
}

// labelSet returns the set of item labels.
func labelSet(items []protocol.CompletionItem) map[string]bool {
	got := make(map[string]bool, len(items))
	for _, it := range items {
		got[it.Label] = true
	}
	return got
}

// expectLabels fails unless every want label is in items.
func expectLabels(t *testing.T, items []protocol.CompletionItem, wants ...string) {
	t.Helper()
	got := labelSet(items)
	var missing []string
	for _, w := range wants {
		if !got[w] {
			missing = append(missing, w)
		}
	}
	if len(missing) > 0 {
		t.Errorf("completion missing %d label(s): %v\ngot: %v", len(missing), missing, got)
	}
}

// expectNoLabels fails if any banned label is in items.
func expectNoLabels(t *testing.T, items []protocol.CompletionItem, banned ...string) {
	t.Helper()
	got := labelSet(items)
	var leaked []string
	for _, w := range banned {
		if got[w] {
			leaked = append(leaked, w)
		}
	}
	if len(leaked) > 0 {
		t.Errorf("completion unexpectedly contains %d banned label(s): %v\ngot: %v", len(leaked), leaked, got)
	}
}

// markCursor removes the cursor mark from src and returns the text and the
// mark's LSP position, its character counted in UTF-16 units.
func markCursor(t *testing.T, src string) (string, protocol.Position) {
	t.Helper()
	i := strings.Index(src, cursorMark)
	if i < 0 {
		t.Fatalf("fixture carries no %q cursor mark", cursorMark)
	}
	head := src[:i]
	return head + src[i+len(cursorMark):], protocol.Position{
		Line:      uint32(strings.Count(head, "\n")),
		Character: uint32(utf16Len(head[strings.LastIndexByte(head, '\n')+1:])),
	}
}

// tokenUnder returns the index and token under pos, or -1.
func tokenUnder(view snapshotView, pos protocol.Position) (int, lexer.Token) {
	c := view.cursorAt(pos)
	if c.at < 0 {
		return -1, lexer.Token{}
	}
	return c.at, view.tokens[c.at]
}

// docAt returns the position params of pos in the document u.
func docAt(u uri.URI, pos protocol.Position) protocol.TextDocumentPositionParams {
	return protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentURI(u)},
		Position:     pos,
	}
}

// rangeText returns the text of src that r covers.
func rangeText(src string, r protocol.Range) string {
	start := offsetFromLSP(src, r.Start.Line, r.Start.Character)
	end := offsetFromLSP(src, r.End.Line, r.End.Character)
	if start > end {
		return "<inverted range>"
	}
	return src[start:end]
}

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

// newTestServer returns a server with no open documents and no connection.
func newTestServer() *server {
	return &server{docs: map[uri.URI]string{}}
}

// bufferDiagnostics returns the diagnostics of src open outside any project.
func bufferDiagnostics(src string) []protocol.Diagnostic {
	u := uri.New("file:///t.craftgo")
	perFile, _ := newTestServer().buildProjectDiagnostics(u, src)
	return perFile[uriToPath(string(u))]
}

// mustWrite writes content to path, creating its parent directories.
func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFileT(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// manifestProject writes a design root with the given manifest body and
// one design file, and returns the file's path.
func manifestProject(t *testing.T, manifest, design string) string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "design", "craftgo.design.yaml"), manifest)
	path := filepath.Join(root, "design", "svc.craftgo")
	mustWrite(t, path, design)
	return path
}

const layoutOnly = `output:
  types:      ./internal/types
  transport:  ./internal/transport
  routes:     ./internal/routes
  service:    ./internal/service
  middleware: ./internal/middleware
  svccontext: ./svccontext/svccontext.go
  openapi:    ./docs/openapi.yaml
openapi:
  title: T
  version: 1.0.0
`
