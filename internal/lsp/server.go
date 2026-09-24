// Package lsp implements the craftgo language server over a stdio stream.
// It keeps only the open buffers: every request parses what it needs again
// and reads the rest of the design project from disk.
package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/craftgodotdev/craftgo/internal/config"
)

// Version is the version the server reports on initialize. Release builds set
// it with `-ldflags -X`, which only writes a var.
var Version = "1.9.0"

// errExitWithoutShutdown reports an `exit` that arrived before `shutdown`;
// LSP requires a non-zero exit status then.
var errExitWithoutShutdown = errors.New("exit notification without prior shutdown")

// Serve speaks LSP over in and out until `exit` or the end of the connection;
// an `exit` without `shutdown` is an error.
func Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	stream := jsonrpc2.NewStream(&stdioRWC{in: in, out: out})
	conn := jsonrpc2.NewConn(stream)
	srv := &server{
		conn: conn,
		docs: make(map[uri.URI]string),
		exit: make(chan struct{}),
	}
	conn.Go(ctx, srv.handler)
	select {
	case <-srv.exit:
		if srv.shutdownRequested() {
			return nil
		}
		return errExitWithoutShutdown
	case <-conn.Done():
		if err := conn.Err(); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		return nil
	}
}

// server is the state of one LSP session; [Serve] builds it.
type server struct {
	conn     jsonrpc2.Conn
	mu       sync.Mutex         // guards docs and shutdown
	docs     map[uri.URI]string // the full text of each open file (full sync)
	exit     chan struct{}
	exitOnce sync.Once
	shutdown bool
}

func (s *server) signalExit() {
	s.exitOnce.Do(func() { close(s.exit) })
}

func (s *server) requestShutdown() {
	s.mu.Lock()
	s.shutdown = true
	s.mu.Unlock()
}

func (s *server) shutdownRequested() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shutdown
}

// stdioRWC joins in and out into the [io.ReadWriteCloser] jsonrpc2 needs.
// Close is a no-op: the caller owns both streams.
type stdioRWC struct {
	in  io.Reader
	out io.Writer
}

func (r *stdioRWC) Read(p []byte) (int, error)  { return r.in.Read(p) }
func (r *stdioRWC) Write(p []byte) (int, error) { return r.out.Write(p) }
func (r *stdioRWC) Close() error                { return nil }

// handler dispatches every inbound message by method; an unknown method
// gets [jsonrpc2.ErrMethodNotFound].
func (s *server) handler(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	switch req.Method() {
	case protocol.MethodInitialize:
		return s.onInitialize(ctx, reply, req)
	case protocol.MethodInitialized:
		return s.onInitialized(ctx, reply, req)
	case protocol.MethodShutdown:
		s.requestShutdown()
		return reply(ctx, nil, nil)
	case protocol.MethodExit:
		err := reply(ctx, nil, nil)
		s.signalExit()
		return err
	case protocol.MethodTextDocumentDidOpen:
		return s.onDidOpen(ctx, reply, req)
	case protocol.MethodTextDocumentDidChange:
		return s.onDidChange(ctx, reply, req)
	case protocol.MethodTextDocumentDidClose:
		return s.onDidClose(ctx, reply, req)
	case protocol.MethodTextDocumentDidSave:
		return s.onDidSave(ctx, reply, req)
	case protocol.MethodWorkspaceDidChangeWatchedFiles:
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

func (s *server) onInitialize(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
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
				TriggerCharacters:   []string{"(", ","},
				RetriggerCharacters: []string{","},
			},
			RenameProvider: &protocol.RenameOptions{PrepareProvider: true},
			CompletionProvider: &protocol.CompletionOptions{
				TriggerCharacters: []string{"@", " ", ",", ".", "/", "{", "\""},
			},
		},
		ServerInfo: &protocol.ServerInfo{
			Name:    "craftgo-lsp",
			Version: Version,
		},
	}, nil)
}

// snapshot returns the open text of u, or "" when u is not open.
func (s *server) snapshot(u uri.URI) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.docs[u]
}

func (s *server) onDidOpen(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	var params protocol.DidOpenTextDocumentParams
	if err := json.Unmarshal(req.Params(), &params); err != nil {
		return reply(ctx, nil, err)
	}
	s.storeDoc(params.TextDocument.URI, params.TextDocument.Text)
	s.publishDiagnostics(ctx, params.TextDocument.URI, params.TextDocument.Text)
	return reply(ctx, nil, nil)
}

func (s *server) onDidChange(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	var params protocol.DidChangeTextDocumentParams
	if err := json.Unmarshal(req.Params(), &params); err != nil {
		return reply(ctx, nil, err)
	}
	if len(params.ContentChanges) == 0 {
		return reply(ctx, nil, nil)
	}
	// Full sync: the last change carries the whole buffer.
	text := params.ContentChanges[len(params.ContentChanges)-1].Text
	s.storeDoc(params.TextDocument.URI, text)
	s.publishDiagnostics(ctx, params.TextDocument.URI, text)
	return reply(ctx, nil, nil)
}

func (s *server) onDidSave(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	var params protocol.DidSaveTextDocumentParams
	if err := json.Unmarshal(req.Params(), &params); err != nil {
		return reply(ctx, nil, err)
	}
	// A save without text re-checks the cached buffer.
	text := params.Text
	if text == "" {
		text = s.snapshot(params.TextDocument.URI)
	} else {
		s.storeDoc(params.TextDocument.URI, text)
	}
	if text != "" {
		s.publishDiagnostics(ctx, params.TextDocument.URI, text)
	}
	return reply(ctx, nil, nil)
}

func (s *server) onDidClose(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	var params protocol.DidCloseTextDocumentParams
	if err := json.Unmarshal(req.Params(), &params); err != nil {
		return reply(ctx, nil, err)
	}
	s.mu.Lock()
	delete(s.docs, params.TextDocument.URI)
	s.mu.Unlock()
	// An empty list clears the closed file's diagnostics.
	_ = s.conn.Notify(ctx, protocol.MethodTextDocumentPublishDiagnostics, &protocol.PublishDiagnosticsParams{
		URI:         params.TextDocument.URI,
		Diagnostics: []protocol.Diagnostic{},
	})
	return reply(ctx, nil, nil)
}

// onInitialized asks the client to watch the design files and the manifest; a
// refusal is ignored. The call runs in a goroutine: the jsonrpc2 read loop is
// single-threaded, so waiting for the reply in the handler would deadlock.
func (s *server) onInitialized(ctx context.Context, reply jsonrpc2.Replier, _ jsonrpc2.Request) error {
	go func() {
		// A closed connection cancels the call, so the goroutine never outlives it.
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

// watchedFilesGlob matches every design-file extension, e.g. `**/*.{craftgo,cg}`.
func watchedFilesGlob() string {
	bare := make([]string, len(config.DesignFileExtensions))
	for i, e := range config.DesignFileExtensions {
		bare[i] = strings.TrimPrefix(e, ".")
	}
	return "**/*.{" + strings.Join(bare, ",") + "}"
}

// manifestGlob matches the manifest, which is an input to the analysis.
func manifestGlob() string {
	return "**/" + config.Filename
}

// watchedFilesRegistration subscribes to create, change and delete events (an
// omitted Kind means all three) for the design files and the manifest.
func watchedFilesRegistration() protocol.RegistrationParams {
	return protocol.RegistrationParams{
		Registrations: []protocol.Registration{{
			ID:     "craftgo-watch-design-files",
			Method: protocol.MethodWorkspaceDidChangeWatchedFiles,
			RegisterOptions: protocol.DidChangeWatchedFilesRegistrationOptions{
				Watchers: []protocol.FileSystemWatcher{
					{GlobPattern: watchedFilesGlob()},
					{GlobPattern: manifestGlob()},
				},
			},
		}},
	}
}

// onDidChangeWatchedFiles re-publishes the diagnostics of every open document
// after a watched file changes on disk.
func (s *server) onDidChangeWatchedFiles(ctx context.Context, reply jsonrpc2.Replier, _ jsonrpc2.Request) error {
	// One publishDiagnostics per design root covers every open file under it.
	seenRoots := map[string]bool{}
	for u := range s.openDocURIs() {
		src := s.snapshot(u)
		if src == "" {
			continue
		}
		if _, root := designProjectOf(uriToPath(string(u))); root != "" {
			if seenRoots[root] {
				continue
			}
			seenRoots[root] = true
		}
		s.publishDiagnostics(ctx, u, src)
	}
	return reply(ctx, nil, nil)
}

// storeDoc records text as the open content of u.
func (s *server) storeDoc(u uri.URI, text string) {
	s.mu.Lock()
	s.docs[u] = text
	s.mu.Unlock()
}

// publishDiagnostics analyses the project of u holding src and publishes the
// diagnostics of u and of every other open file under the same design root.
func (s *server) publishDiagnostics(ctx context.Context, u uri.URI, src string) {
	perFile, designRoot := s.buildProjectDiagnostics(u, src)
	path := uriToPath(string(u))
	_ = s.conn.Notify(ctx, protocol.MethodTextDocumentPublishDiagnostics, &protocol.PublishDiagnosticsParams{
		URI:         u,
		Diagnostics: diagsFor(perFile, path),
	})
	pushed := map[string]bool{path: true}
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

// diagsFor returns the diagnostics of key, never nil: clients ignore a null
// list but clear a file on `[]`.
func diagsFor(perFile map[string][]protocol.Diagnostic, key string) []protocol.Diagnostic {
	if d := perFile[key]; d != nil {
		return d
	}
	return []protocol.Diagnostic{}
}

// openDocURIs returns the URIs of the open documents.
func (s *server) openDocURIs() map[uri.URI]struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[uri.URI]struct{}, len(s.docs))
	for k := range s.docs {
		out[k] = struct{}{}
	}
	return out
}
