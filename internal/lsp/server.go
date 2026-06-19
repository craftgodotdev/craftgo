// Package lsp implements the CraftGo Language Server Protocol surface.
//
// The server speaks LSP over stdio (a [jsonrpc2.Stream] wrapped around the
// caller's [io.Reader] / [io.Writer]) and forwards each open document
// through the existing parser + semantic analyser. Diagnostics published by
// the server are exactly the diagnostics the CLI would emit for the same
// source, so editor and CLI behaviour stay aligned by construction.
//
// Currently supported:
//
//   - initialize / initialized / shutdown / exit lifecycle
//   - textDocument/didOpen / didChange / didSave / didClose
//   - textDocument/publishDiagnostics on every successful parse pass
//   - textDocument/hover (decorator, type ref, builtin docs)
//   - textDocument/completion (decorators, types, fields)
//   - textDocument/definition (cross-file decl resolution)
//   - textDocument/references (find all uses)
//   - textDocument/documentSymbol (outline)
//   - textDocument/formatting (canonical re-print via internal/format)
//   - textDocument/rename (declarations + every reference)
//
// Any other request returns [jsonrpc2.ErrMethodNotFound], which clients
// treat as "feature unsupported".
package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/craftgodotdev/craftgo/internal/config"
)

// Version is the server's reported version, surfaced via Initialize so
// clients can include it in trace logs. The source value is the fallback for
// `go install`; release builds inject the git tag via
// `-ldflags="-X ...internal/lsp.Version=<tag>"` (see .goreleaser.yaml), so it
// must be a var - `-X` cannot write a const.
var Version = "1.4.2"

// Serve runs the LSP loop on the supplied stdio streams. It blocks until
// the peer closes the connection or context is cancelled, and returns the
// terminating error (nil on a clean shutdown).
func Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	stream := jsonrpc2.NewStream(&stdioRWC{in: in, out: out})
	conn := jsonrpc2.NewConn(stream)
	srv := &Server{
		conn: conn,
		docs: make(map[uri.URI]*document),
	}
	conn.Go(ctx, srv.handler)
	<-conn.Done()
	if err := conn.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// Server holds the server-side state. The zero value is not useful - call
// [Serve] which constructs and wires one for the duration of the session.
type Server struct {
	conn jsonrpc2.Conn
	mu   sync.Mutex
	docs map[uri.URI]*document
}

// document caches the latest content of an open file. The version is the
// LSP-supplied counter the editor uses to keep us in sync; we accept full
// syncs only (TextDocumentSyncKindFull), so each didChange replaces text
// wholesale rather than applying incremental edits.
type document struct {
	text    string
	version int32
}

// stdioRWC adapts a separate [io.Reader] and [io.Writer] into the
// [io.ReadWriteCloser] that jsonrpc2 expects. Close is a no-op because
// stdio descriptors are owned by the parent process.
type stdioRWC struct {
	in  io.Reader
	out io.Writer
}

func (r *stdioRWC) Read(p []byte) (int, error)  { return r.in.Read(p) }
func (r *stdioRWC) Write(p []byte) (int, error) { return r.out.Write(p) }
func (r *stdioRWC) Close() error                { return nil }

// handler is the single entry point the jsonrpc2 layer calls for every
// inbound message. It dispatches by method name; each case decodes the
// concrete params struct, performs the work, and replies.
func (s *Server) handler(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	switch req.Method() {
	case protocol.MethodInitialize:
		return s.onInitialize(ctx, reply, req)
	case protocol.MethodInitialized:
		return s.onInitialized(ctx, reply, req)
	case protocol.MethodShutdown:
		return reply(ctx, nil, nil)
	case protocol.MethodExit:
		_ = s.conn.Close()
		return reply(ctx, nil, nil)
	case protocol.MethodTextDocumentDidOpen:
		return s.onDidOpen(ctx, reply, req)
	case protocol.MethodTextDocumentDidChange:
		return s.onDidChange(ctx, reply, req)
	case protocol.MethodTextDocumentDidClose:
		return s.onDidClose(ctx, reply, req)
	case protocol.MethodTextDocumentDidSave:
		// Re-validate on save in case the editor sent a "save without
		// change" event (e.g. external formatter rewrote the file).
		return s.onDidSave(ctx, reply, req)
	case protocol.MethodWorkspaceDidChangeWatchedFiles:
		// A `.craftgo` file was created / deleted / changed on disk (possibly
		// outside the editor, or a file the user never opened). Cross-package
		// resolution re-reads the disk per request, but the diagnostics on
		// already-open files are only refreshed when those files are edited -
		// so re-run them now against the new project state.
		return s.onDidChangeWatchedFiles(ctx, reply, req)
	case protocol.MethodTextDocumentHover:
		return s.onHover(ctx, reply, req)
	case protocol.MethodTextDocumentCompletion:
		return s.onCompletion(ctx, reply, req)
	case protocol.MethodTextDocumentDefinition:
		return s.onDefinition(ctx, reply, req)
	case protocol.MethodTextDocumentReferences:
		return s.onReferences(ctx, reply, req)
	case protocol.MethodTextDocumentDocumentSymbol:
		return s.onDocumentSymbol(ctx, reply, req)
	case protocol.MethodTextDocumentFormatting:
		return s.onFormatting(ctx, reply, req)
	case protocol.MethodTextDocumentPrepareRename:
		return s.onPrepareRename(ctx, reply, req)
	case protocol.MethodTextDocumentRename:
		return s.onRename(ctx, reply, req)
	case protocol.MethodTextDocumentDocumentHighlight:
		return s.onDocumentHighlight(ctx, reply, req)
	case protocol.MethodTextDocumentSignatureHelp:
		return s.onSignatureHelp(ctx, reply, req)
	case protocol.MethodWorkspaceSymbol:
		return s.onWorkspaceSymbol(ctx, reply, req)
	default:
		return reply(ctx, nil, fmt.Errorf("%q: %w", req.Method(), jsonrpc2.ErrMethodNotFound))
	}
}

func (s *Server) onInitialize(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	var params protocol.InitializeParams
	if err := json.Unmarshal(req.Params(), &params); err != nil {
		return reply(ctx, nil, err)
	}
	return reply(ctx, &protocol.InitializeResult{
		Capabilities: protocol.ServerCapabilities{
			TextDocumentSync:           protocol.TextDocumentSyncKindFull,
			HoverProvider:              true,
			DefinitionProvider:         true,
			ReferencesProvider:         true,
			DocumentSymbolProvider:     true,
			WorkspaceSymbolProvider:    true,
			DocumentHighlightProvider:  true,
			DocumentFormattingProvider: true,
			SignatureHelpProvider: &protocol.SignatureHelpOptions{
				// `(` opens a decorator-argument list, `,` advances to
				// the next parameter slot - both should re-fetch
				// signature help so the active parameter highlight
				// follows the cursor.
				TriggerCharacters:   []string{"(", ","},
				RetriggerCharacters: []string{","},
			},
			RenameProvider: &protocol.RenameOptions{PrepareProvider: true},
			CompletionProvider: &protocol.CompletionOptions{
				// Generous trigger set so completion auto-fires at
				// every transition the user is likely to want help
				// at: decorator start (`@`), token boundary
				// (space, comma), qualified ref (`.`), path segment
				// (`/`), brace open (`{`), and string open (`"`)
				// for `import "..."` paths. Identifier-letter
				// triggering is delegated to VSCode's
				// `editor.quickSuggestions.other` (set in the
				// extension's configurationDefaults).
				TriggerCharacters: []string{"@", " ", ",", ".", "/", "{", "\""},
			},
		},
		ServerInfo: &protocol.ServerInfo{
			Name:    "craftgo-lsp",
			Version: Version,
		},
	}, nil)
}

// snapshot returns the cached text for u (and an empty string when the
// document has not been opened). All feature handlers go through this so
// they can short-circuit when the editor has already closed the file.
func (s *Server) snapshot(u uri.URI) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.docs[u]; ok {
		return d.text
	}
	return ""
}

func (s *Server) onDidOpen(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	var params protocol.DidOpenTextDocumentParams
	if err := json.Unmarshal(req.Params(), &params); err != nil {
		return reply(ctx, nil, err)
	}
	s.storeDoc(params.TextDocument.URI, params.TextDocument.Text, params.TextDocument.Version)
	s.publishDiagnostics(ctx, params.TextDocument.URI, params.TextDocument.Text)
	return reply(ctx, nil, nil)
}

func (s *Server) onDidChange(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	var params protocol.DidChangeTextDocumentParams
	if err := json.Unmarshal(req.Params(), &params); err != nil {
		return reply(ctx, nil, err)
	}
	if len(params.ContentChanges) == 0 {
		return reply(ctx, nil, nil)
	}
	// Full-sync mode - the editor sends one change containing the entire
	// new buffer. The last change wins if multiple are batched (defensive).
	text := params.ContentChanges[len(params.ContentChanges)-1].Text
	s.storeDoc(params.TextDocument.URI, text, params.TextDocument.Version)
	s.publishDiagnostics(ctx, params.TextDocument.URI, text)
	return reply(ctx, nil, nil)
}

func (s *Server) onDidSave(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	var params protocol.DidSaveTextDocumentParams
	if err := json.Unmarshal(req.Params(), &params); err != nil {
		return reply(ctx, nil, err)
	}
	// If the save event includes the post-save text, refresh the cache
	// before re-validating; otherwise re-use whatever we already have.
	text := params.Text
	if text == "" {
		s.mu.Lock()
		if d, ok := s.docs[params.TextDocument.URI]; ok {
			text = d.text
		}
		s.mu.Unlock()
	} else {
		s.storeDoc(params.TextDocument.URI, text, 0)
	}
	if text != "" {
		s.publishDiagnostics(ctx, params.TextDocument.URI, text)
	}
	return reply(ctx, nil, nil)
}

func (s *Server) onDidClose(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	var params protocol.DidCloseTextDocumentParams
	if err := json.Unmarshal(req.Params(), &params); err != nil {
		return reply(ctx, nil, err)
	}
	s.mu.Lock()
	delete(s.docs, params.TextDocument.URI)
	s.mu.Unlock()
	// Clear diagnostics so the editor doesn't keep stale squigglies on a
	// file we are no longer tracking.
	_ = s.conn.Notify(ctx, protocol.MethodTextDocumentPublishDiagnostics, &protocol.PublishDiagnosticsParams{
		URI:         params.TextDocument.URI,
		Diagnostics: []protocol.Diagnostic{},
	})
	return reply(ctx, nil, nil)
}

// onInitialized registers a workspace file watcher for `**/*.craftgo` so the
// client forwards create / change / delete events for every design file -
// including files the user has not opened and changes made outside the editor.
// Registration is best-effort: a client without dynamic-registration support
// rejects it, and on-demand features re-walk the disk regardless, so a failure
// is non-fatal. The `client/registerCapability` request is sent from a
// goroutine because the jsonrpc2 read loop is single-threaded - blocking the
// handler on the client's reply here would deadlock the connection.
func (s *Server) onInitialized(ctx context.Context, reply jsonrpc2.Replier, _ jsonrpc2.Request) error {
	go func() {
		// ctx is the connection-scoped handler context; derive a child that
		// also unblocks the Call when the connection itself tears down (a
		// client EOF cancels conn.Done() but not necessarily ctx), so the
		// goroutine can never outlive the session waiting on a reply.
		callCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			select {
			case <-s.conn.Done():
				cancel()
			case <-callCtx.Done():
			}
		}()
		_, _ = s.conn.Call(callCtx, protocol.MethodClientRegisterCapability, watchedFilesRegistration(), nil)
	}()
	return reply(ctx, nil, nil)
}

// watchedFilesGlob builds the workspace file-watch pattern from the canonical
// source extensions - e.g. `**/*.{craftgo,cg}`. Brace groups are part of the
// LSP glob syntax, so a single watcher covers every accepted extension.
func watchedFilesGlob() string {
	bare := make([]string, len(config.DesignFileExtensions))
	for i, e := range config.DesignFileExtensions {
		bare[i] = strings.TrimPrefix(e, ".")
	}
	return "**/*.{" + strings.Join(bare, ",") + "}"
}

// watchedFilesRegistration is the `client/registerCapability` payload that
// subscribes the server to create / change / delete events for every craftgo
// source file (Kind omitted → the client watches all three).
func watchedFilesRegistration() protocol.RegistrationParams {
	return protocol.RegistrationParams{
		Registrations: []protocol.Registration{{
			ID:     "craftgo-watch-design-files",
			Method: protocol.MethodWorkspaceDidChangeWatchedFiles,
			RegisterOptions: protocol.DidChangeWatchedFilesRegistrationOptions{
				Watchers: []protocol.FileSystemWatcher{{GlobPattern: watchedFilesGlob()}},
			},
		}},
	}
}

// onDidChangeWatchedFiles refreshes diagnostics for every open document after a
// `.craftgo` file changed on disk. The fresh pass re-walks the project, so a
// deleted type stops resolving (and a re-added one resolves again) in the
// squigglies of dependent open files without the user touching them.
func (s *Server) onDidChangeWatchedFiles(ctx context.Context, reply jsonrpc2.Replier, _ jsonrpc2.Request) error {
	// publishDiagnostics already re-analyses the whole project and re-publishes
	// every OTHER open file under the trigger's design root, so calling it once
	// per distinct root (not once per open doc) refreshes everything while
	// avoiding an N-times disk re-walk and N*N notifications. Docs with no
	// resolvable root fall through to publishDiagnostics's single-file path.
	seenRoots := map[string]bool{}
	for u := range s.openDocURIs() {
		src := s.snapshot(u)
		if src == "" {
			continue
		}
		if path := uriToPath(string(u)); path != "" {
			if _, _, root, err := config.Find(filepath.Dir(path)); err == nil && root != "" {
				if seenRoots[root] {
					continue
				}
				seenRoots[root] = true
			}
		}
		s.publishDiagnostics(ctx, u, src)
	}
	return reply(ctx, nil, nil)
}

// storeDoc replaces the cached entry for u with the given text+version. It
// is safe to call from any handler; it acquires the document mutex briefly.
func (s *Server) storeDoc(u uri.URI, text string, version int32) {
	s.mu.Lock()
	s.docs[u] = &document{text: text, version: version}
	s.mu.Unlock()
}

// publishDiagnostics parses src and pushes the resulting diagnostics back
// to the client as a textDocument/publishDiagnostics notification. It does
// not return an error - diagnostic publishing is best-effort, and a
// failed notify is logged via the connection's done channel.
//
// In project mode the edit may have (in)validated diagnostics in OTHER
// open files (e.g. adding a field to a request type clears the
// "path segment has no matching field" error in the service file that
// references it). To avoid stale squigglies, the resulting per-file
// diagnostics are pushed for every open file in the same project, not
// just the triggering URI. Single-file mode pushes only for u.
func (s *Server) publishDiagnostics(ctx context.Context, u uri.URI, src string) {
	perFile, designRoot := s.buildProjectDiagnostics(u, src)
	if designRoot == "" {
		// Single-file fallback - the project analyser didn't run.
		_ = s.conn.Notify(ctx, protocol.MethodTextDocumentPublishDiagnostics, &protocol.PublishDiagnosticsParams{
			URI:         u,
			Diagnostics: diagsFor(perFile, uriToPath(string(u))),
		})
		return
	}
	// Always push for u (handles the "edit cleared all diags" case).
	pushed := map[string]bool{uriToPath(string(u)): true}
	_ = s.conn.Notify(ctx, protocol.MethodTextDocumentPublishDiagnostics, &protocol.PublishDiagnosticsParams{
		URI:         u,
		Diagnostics: diagsFor(perFile, uriToPath(string(u))),
	})
	// Republish every OTHER open file that lives under the same design
	// root. Empty payloads clear stale squigglies in dependent files.
	for openURI := range s.openDocURIs() {
		op := uriToPath(string(openURI))
		if op == "" || pushed[op] || !isUnderDesignRoot(op, designRoot) {
			continue
		}
		pushed[op] = true
		_ = s.conn.Notify(ctx, protocol.MethodTextDocumentPublishDiagnostics, &protocol.PublishDiagnosticsParams{
			URI:         openURI,
			Diagnostics: diagsFor(perFile, op),
		})
	}
}

// diagsFor looks up a per-file partition and ALWAYS returns a non-nil
// slice. nil JSON-marshals to `null`, which several LSP clients treat as
// "ignore" rather than "clear diagnostics for this file" - so we have to
// hand them an explicit `[]` to clear stale squigglies.
func diagsFor(perFile map[string][]protocol.Diagnostic, key string) []protocol.Diagnostic {
	if d := perFile[key]; d != nil {
		return d
	}
	return []protocol.Diagnostic{}
}

// openDocURIs returns a snapshot of every currently-open document URI.
// Used by publishDiagnostics to know which sibling files need their
// diagnostics refreshed after a cross-file edit.
func (s *Server) openDocURIs() map[uri.URI]struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[uri.URI]struct{}, len(s.docs))
	for k := range s.docs {
		out[k] = struct{}{}
	}
	return out
}
