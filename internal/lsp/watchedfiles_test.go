package lsp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// A file deleted from disk while open stays in the project with its buffer.
func TestDeletedButOpenFileStaysVisible(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "design", "craftgo.design.yaml"), `output:
  types:      ./internal/types
  transport:  ./internal/transport
  routes:     ./internal/routes
  service:    ./internal/service
  svccontext: ./svccontext/svccontext.go
  openapi:    ./docs/openapi.yaml
openapi:
  title: T
  version: 1.0.0
  basePath: /api
`)
	aPath := filepath.Join(root, "design", "things", "a.craftgo")
	bPath := filepath.Join(root, "design", "things", "b.craftgo")
	// A declares the type B uses and has an error of its own (unknown Nope).
	mustWrite(t, aPath, "package things\ntype A {}\ntype HasErr { x Nope }\n")
	mustWrite(t, bPath, "package things\ntype B { a A }\n")

	s := newTestServer()
	aURI := uri.File(aPath)
	bURI := uri.File(bPath)
	aSrc := readFileT(t, aPath)
	s.storeDoc(aURI, aSrc)
	s.storeDoc(bURI, readFileT(t, bPath))

	// Delete A on disk while it stays open in the editor.
	if err := os.Remove(aPath); err != nil {
		t.Fatal(err)
	}

	// Analysing from B still sees A's buffer.
	perFile, designRoot := s.buildProjectDiagnostics(bURI, readFileT(t, bPath))
	if designRoot == "" {
		t.Fatal("expected project mode")
	}
	if n := len(perFile[bPath]); n != 0 {
		t.Errorf("B should not report errors while A is still open: %+v", perFile[bPath])
	}
	if len(perFile[aPath]) == 0 {
		t.Error("A's own diagnostics (unknown Nope) should survive deletion while open")
	}
}

// recordingConn is a jsonrpc2.Conn that records the methods sent to the
// client and the diagnostics published.
type recordingConn struct {
	mu        sync.Mutex
	notifies  []string
	calls     []string
	published []*protocol.PublishDiagnosticsParams
}

func (c *recordingConn) Notify(_ context.Context, method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notifies = append(c.notifies, method)
	if p, ok := params.(*protocol.PublishDiagnosticsParams); ok {
		c.published = append(c.published, p)
	}
	return nil
}

// lastPublished returns the diagnostics last published for u, and whether
// any were.
func (c *recordingConn) lastPublished(u uri.URI) ([]protocol.Diagnostic, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.published) - 1; i >= 0; i-- {
		if c.published[i].URI == u {
			return c.published[i].Diagnostics, true
		}
	}
	return nil, false
}

func (c *recordingConn) Call(_ context.Context, method string, _, _ any) (jsonrpc2.ID, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, method)
	return jsonrpc2.ID{}, nil
}

func (c *recordingConn) Go(context.Context, jsonrpc2.Handler) {}
func (c *recordingConn) Close() error                         { return nil }
func (c *recordingConn) Done() <-chan struct{}                { return nil }
func (c *recordingConn) Err() error                           { return nil }

func (c *recordingConn) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

func (c *recordingConn) notifyCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.notifies)
}

// A manifest key craftgo does not read shows as a warning on the manifest,
// published with the diagnostics of its design and cleared once it goes.
func TestManifestWarningsShowOnTheManifest(t *testing.T) {
	path := manifestProject(t, layoutOnly+"typo: 1\n", "package svc\n")
	manifest := filepath.Join(filepath.Dir(path), "craftgo.design.yaml")
	conn := &recordingConn{}
	s := &server{docs: map[uri.URI]string{}, conn: conn}
	s.publishDiagnostics(context.Background(), uri.File(path), readFileT(t, path))
	got, ok := conn.lastPublished(uri.File(manifest))
	if !ok || len(got) != 1 || got[0].Severity != protocol.DiagnosticSeverityWarning || !strings.Contains(got[0].Message, "typo") {
		t.Fatalf("manifest diagnostics = %+v (published %v), want the typo warning", got, ok)
	}
	mustWrite(t, manifest, layoutOnly)
	s.publishDiagnostics(context.Background(), uri.File(path), readFileT(t, path))
	if got, _ := conn.lastPublished(uri.File(manifest)); got == nil || len(got) != 0 {
		t.Errorf("manifest diagnostics after the fix = %+v, want an empty list", got)
	}
}

// A basePath problem shows once on the manifest, not on the design file.
func TestBasePathDiagnosticsShowOnTheManifest(t *testing.T) {
	path := manifestProject(t, layoutOnly+"  basePath: \"/t/{a-b}/{a-b}\"\n", "package svc\n")
	manifest := filepath.Join(filepath.Dir(path), "craftgo.design.yaml")
	conn := &recordingConn{}
	s := &server{docs: map[uri.URI]string{}, conn: conn}
	s.publishDiagnostics(context.Background(), uri.File(path), readFileT(t, path))
	if got, _ := conn.lastPublished(uri.File(manifest)); len(got) != 1 || !strings.Contains(got[0].Message, "openapi.basePath") {
		t.Errorf("manifest diagnostics = %+v, want the one basePath error", got)
	}
	if got, _ := conn.lastPublished(uri.File(path)); len(got) != 0 {
		t.Errorf("design file diagnostics = %+v, want none", got)
	}
}

// A manifest edited so it no longer loads shows the error in place of its
// warnings, and one fixed while no design file is open is cleared.
func TestManifestDiagnosticsFollowTheManifest(t *testing.T) {
	path := manifestProject(t, layoutOnly+"typo: 1\n", "package svc\n")
	manifest := filepath.Join(filepath.Dir(path), "craftgo.design.yaml")
	u := uri.File(path)
	conn := &recordingConn{}
	s := &server{docs: map[uri.URI]string{}, conn: conn}
	ctx := context.Background()
	s.storeDoc(u, readFileT(t, path))
	s.publishDiagnostics(ctx, u, readFileT(t, path))
	mustWrite(t, manifest, layoutOnly+"design: ./x\n")
	s.onDidChangeWatchedFiles(ctx)
	got, _ := conn.lastPublished(uri.File(manifest))
	if len(got) != 1 || got[0].Severity != protocol.DiagnosticSeverityError || !strings.Contains(got[0].Message, "design") {
		t.Errorf("manifest diagnostics after a removed key = %+v, want its error", got)
	}
	if _, err := callHandler(t, s, protocol.MethodTextDocumentDidClose, protocol.DidCloseTextDocumentParams{TextDocument: protocol.TextDocumentIdentifier{URI: u}}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, manifest, layoutOnly)
	s.onDidChangeWatchedFiles(ctx)
	if got, _ := conn.lastPublished(uri.File(manifest)); got == nil || len(got) != 0 {
		t.Errorf("manifest diagnostics after the fix = %+v, want an empty list", got)
	}
}

// Closing an edited buffer unsaved re-checks the other open files of its root
// against the file on disk.
func TestCloseUnsavedBufferRechecksTheRoot(t *testing.T) {
	aPath := manifestProject(t, layoutOnly, "package svc\ntype User { id string }\n")
	bPath := filepath.Join(filepath.Dir(aPath), "b.craftgo")
	mustWrite(t, bPath, "package svc\ntype Team { lead User }\n")
	aURI, bURI := uri.File(aPath), uri.File(bPath)
	conn := &recordingConn{}
	s := &server{docs: map[uri.URI]string{}, conn: conn}
	ctx := context.Background()
	s.storeDoc(bURI, readFileT(t, bPath))
	edited := "package svc\ntype Member { id string }\n"
	s.storeDoc(aURI, edited)
	s.publishDiagnostics(ctx, aURI, edited)
	if got, _ := conn.lastPublished(bURI); len(got) == 0 {
		t.Fatal("b should report the unknown User while a's buffer renames it")
	}
	if _, err := callHandler(t, s, protocol.MethodTextDocumentDidClose, protocol.DidCloseTextDocumentParams{TextDocument: protocol.TextDocumentIdentifier{URI: aURI}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := conn.lastPublished(bURI); got == nil || len(got) != 0 {
		t.Errorf("b diagnostics after a closed unsaved = %+v, want an empty list", got)
	}
	if got, _ := conn.lastPublished(aURI); got == nil || len(got) != 0 {
		t.Errorf("a diagnostics after close = %+v, want an empty list", got)
	}
}

// The registration watches every design-file extension and the manifest.
func TestWatchedFilesRegistration(t *testing.T) {
	reg := watchedFilesRegistration()
	if len(reg.Registrations) != 1 {
		t.Fatalf("want 1 registration, got %d", len(reg.Registrations))
	}
	r := reg.Registrations[0]
	if r.Method != protocol.MethodWorkspaceDidChangeWatchedFiles {
		t.Errorf("method = %q, want workspace/didChangeWatchedFiles", r.Method)
	}
	opts, ok := r.RegisterOptions.(protocol.DidChangeWatchedFilesRegistrationOptions)
	if !ok || len(opts.Watchers) != 2 {
		t.Fatalf("register options malformed: %#v", r.RegisterOptions)
	}
	globs := []string{opts.Watchers[0].GlobPattern, opts.Watchers[1].GlobPattern}
	want := []string{"**/*.{craftgo,cg}", "**/craftgo.design.yaml"}
	for i := range want {
		if globs[i] != want[i] {
			t.Errorf("glob %d = %q, want %q", i, globs[i], want[i])
		}
	}
}

// onInitialized sends client/registerCapability.
func TestOnInitializedRegistersWatcher(t *testing.T) {
	conn := &recordingConn{}
	s := &server{docs: map[uri.URI]string{}, conn: conn}
	s.onInitialized(context.Background())
	// The Call runs in a goroutine; poll briefly.
	deadline := time.Now().Add(2 * time.Second)
	for conn.callCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if conn.callCount() == 0 {
		t.Fatal("expected a client/registerCapability call")
	}
	if conn.calls[0] != protocol.MethodClientRegisterCapability {
		t.Errorf("call = %q, want client/registerCapability", conn.calls[0])
	}
}

// A watched-file event re-publishes the diagnostics of every open document.
func TestOnDidChangeWatchedFilesRepublishes(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "design", "craftgo.design.yaml"), `output:
  types:      ./internal/types
  transport:  ./internal/transport
  routes:     ./internal/routes
  service:    ./internal/service
  svccontext: ./svccontext/svccontext.go
  openapi:    ./docs/openapi.yaml
openapi:
  title: T
  version: 1.0.0
  basePath: /api
`)
	aPath := filepath.Join(root, "design", "things", "a.craftgo")
	bPath := filepath.Join(root, "design", "things", "b.craftgo")
	mustWrite(t, aPath, "package things\ntype A { id string }\n")
	mustWrite(t, bPath, "package things\ntype B { id string }\n")

	conn := &recordingConn{}
	s := &server{docs: map[uri.URI]string{}, conn: conn}
	s.storeDoc(uri.File(aPath), readFileT(t, aPath))
	s.storeDoc(uri.File(bPath), readFileT(t, bPath))

	s.onDidChangeWatchedFiles(context.Background())
	// One root: each open document and the manifest are published once.
	if n := conn.notifyCount(); n != 3 {
		t.Errorf("expected exactly 3 publishDiagnostics notifications (one per open doc and the manifest, one analysis), got %d", n)
	}
}
