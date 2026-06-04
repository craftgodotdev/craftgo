package lsp

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// TestDeletedButOpenFileStaysVisible pins the fix for the watched-files refresh
// exposing a project-walk gap: a file deleted from disk while still open in the
// editor must keep contributing its live buffer, so dependent files do not get
// spurious "unknown type" errors and the file itself keeps its own diagnostics.
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
	// A defines type A (used by B) AND has its own real error (unknown Nope).
	mustWrite(t, aPath, "package things\ntype A {}\ntype HasErr { x Nope }\n")
	mustWrite(t, bPath, "package things\ntype B { a A }\n")

	s := &Server{docs: map[uri.URI]*document{}}
	aURI := uri.New("file://" + aPath)
	bURI := uri.New("file://" + bPath)
	aSrc := readFileT(t, aPath)
	s.storeDoc(aURI, aSrc, 1)
	s.storeDoc(bURI, readFileT(t, bPath), 1)

	// Delete A on disk while it stays open in the editor.
	if err := os.Remove(aPath); err != nil {
		t.Fatal(err)
	}

	// Trigger via B (still on disk); the project pass must still see A's buffer.
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

// recordingConn is a jsonrpc2.Conn that records the methods sent to the client
// so a handler's outbound notifications / requests can be asserted.
type recordingConn struct {
	mu       sync.Mutex
	notifies []string
	calls    []string
}

func (c *recordingConn) Notify(_ context.Context, method string, _ any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notifies = append(c.notifies, method)
	return nil
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

// TestWatchedFilesRegistration pins the registration payload: it subscribes to
// the design-file glob under the workspace-watcher capability.
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
	if !ok || len(opts.Watchers) != 1 {
		t.Fatalf("register options malformed: %#v", r.RegisterOptions)
	}
	if got := opts.Watchers[0].GlobPattern; got != "**/*.craftgo" {
		t.Errorf("glob = %q, want **/*.craftgo", got)
	}
}

// TestOnInitializedRegistersWatcher confirms the initialized handler sends a
// client/registerCapability request (from its goroutine).
func TestOnInitializedRegistersWatcher(t *testing.T) {
	conn := &recordingConn{}
	s := &Server{docs: map[uri.URI]*document{}, conn: conn}
	if err := s.onInitialized(context.Background(), func(context.Context, any, error) error { return nil }, nil); err != nil {
		t.Fatal(err)
	}
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

// TestOnDidChangeWatchedFilesRepublishes confirms a watched-file event
// re-publishes diagnostics for every open document against the fresh project
// state on disk.
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
	s := &Server{docs: map[uri.URI]*document{}, conn: conn}
	s.storeDoc(uri.New("file://"+aPath), readFileT(t, aPath), 1)
	s.storeDoc(uri.New("file://"+bPath), readFileT(t, bPath), 1)

	if err := s.onDidChangeWatchedFiles(context.Background(), func(context.Context, any, error) error { return nil }, nil); err != nil {
		t.Fatal(err)
	}
	// Both docs share one design root, so the handler analyses once and the
	// single fan-out publishes each open doc exactly once: 2 notifications, not
	// the 4 (N*N) an un-deduped per-doc loop would emit.
	if n := conn.notifyCount(); n != 2 {
		t.Errorf("expected exactly 2 publishDiagnostics notifications (one per open doc, one analysis), got %d", n)
	}
}
