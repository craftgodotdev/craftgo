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
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/designopts"
)

// errExitWithoutShutdown reports an `exit` that arrived before `shutdown`;
// LSP requires a non-zero exit status then.
var errExitWithoutShutdown = errors.New("exit notification without prior shutdown")

// Serve speaks LSP over in and out until `exit` or the end of the connection,
// reporting version on initialize; an `exit` without `shutdown` is an error.
func Serve(ctx context.Context, in io.Reader, out io.Writer, version string) error {
	stream := jsonrpc2.NewStream(&stdioRWC{in: in, out: out})
	conn := jsonrpc2.NewConn(stream)
	srv := &server{
		conn:    conn,
		version: version,
		docs:    make(map[uri.URI]string),
		exit:    make(chan struct{}),
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
	conn      jsonrpc2.Conn
	version   string
	mu        sync.Mutex         // guards docs, manifests and shutdown
	docs      map[uri.URI]string // the full text of each open file (full sync)
	manifests map[string]bool    // the manifests whose diagnostics were published
	exit      chan struct{}
	exitOnce  sync.Once
	shutdown  bool
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
		return handle(ctx, reply, req, s.onInitialize)
	case protocol.MethodInitialized:
		s.onInitialized(ctx)
		return reply(ctx, nil, nil)
	case protocol.MethodShutdown:
		s.requestShutdown()
		return reply(ctx, nil, nil)
	case protocol.MethodExit:
		err := reply(ctx, nil, nil)
		s.signalExit()
		return err
	case protocol.MethodTextDocumentDidOpen:
		return handle(ctx, reply, req, s.onDidOpen)
	case protocol.MethodTextDocumentDidChange:
		return handle(ctx, reply, req, s.onDidChange)
	case protocol.MethodTextDocumentDidClose:
		return handle(ctx, reply, req, s.onDidClose)
	case protocol.MethodTextDocumentDidSave:
		return handle(ctx, reply, req, s.onDidSave)
	case protocol.MethodWorkspaceDidChangeWatchedFiles:
		s.onDidChangeWatchedFiles(ctx)
		return reply(ctx, nil, nil)
	case protocol.MethodTextDocumentHover:
		return handle(ctx, reply, req, s.onHover)
	case protocol.MethodTextDocumentCompletion:
		return handle(ctx, reply, req, s.onCompletion)
	case protocol.MethodTextDocumentDefinition:
		return handle(ctx, reply, req, s.onDefinition)
	case protocol.MethodTextDocumentReferences:
		return handle(ctx, reply, req, s.onReferences)
	case protocol.MethodTextDocumentDocumentSymbol:
		return handle(ctx, reply, req, s.onDocumentSymbol)
	case protocol.MethodTextDocumentFormatting:
		return handle(ctx, reply, req, s.onFormatting)
	case protocol.MethodTextDocumentPrepareRename:
		return handle(ctx, reply, req, s.onPrepareRename)
	case protocol.MethodTextDocumentRename:
		return handle(ctx, reply, req, s.onRename)
	case protocol.MethodTextDocumentDocumentHighlight:
		return handle(ctx, reply, req, s.onDocumentHighlight)
	case protocol.MethodTextDocumentSignatureHelp:
		return handle(ctx, reply, req, s.onSignatureHelp)
	case protocol.MethodWorkspaceSymbol:
		return handle(ctx, reply, req, s.onWorkspaceSymbol)
	default:
		return reply(ctx, nil, fmt.Errorf("%q: %w", req.Method(), jsonrpc2.ErrMethodNotFound))
	}
}

// handle decodes the params of req into P, runs fn on them and replies with
// its result; params that do not decode are the reply's error.
func handle[P any](ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request, fn func(context.Context, P) (any, error)) error {
	var params P
	if err := json.Unmarshal(req.Params(), &params); err != nil {
		return reply(ctx, nil, err)
	}
	result, err := fn(ctx, params)
	return reply(ctx, result, err)
}

// request is one request on an open buffer. The buffer is parsed once: by
// the project when [request.project] runs first, else on its own.
type request struct {
	s      *server
	uri    protocol.DocumentURI
	path   string // "" for a buffer with no file
	src    string
	parsed *snapshotView
	proj   *projectView
}

// open returns the request on the buffer at u, or false when u is not open.
func (s *server) open(u protocol.DocumentURI) (*request, bool) {
	src, ok := s.snapshot(u)
	if !ok {
		return nil, false
	}
	return &request{s: s, uri: u, path: uriToPath(string(u)), src: src}, true
}

// view returns the parsed buffer.
func (r *request) view() snapshotView {
	if r.parsed == nil {
		var v snapshotView
		if r.proj != nil {
			v = r.proj.buffer()
		} else {
			v = parseSnapshot(r.path, r.src)
		}
		r.parsed = &v
	}
	return *r.parsed
}

// project returns the analysed project of the buffer, loaded on first use.
func (r *request) project() projectView {
	if r.proj == nil {
		v := r.s.loadProject(r.path, r.src)
		r.proj = &v
	}
	return *r.proj
}

func (s *server) onInitialize(_ context.Context, _ protocol.InitializeParams) (any, error) {
	return &protocol.InitializeResult{
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
			Version: s.version,
		},
	}, nil
}

// snapshot returns the open text of u and whether u is open.
func (s *server) snapshot(u uri.URI) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	text, ok := s.docs[u]
	return text, ok
}

func (s *server) onDidOpen(ctx context.Context, params protocol.DidOpenTextDocumentParams) (any, error) {
	s.storeDoc(params.TextDocument.URI, params.TextDocument.Text)
	s.publishDiagnostics(ctx, params.TextDocument.URI, params.TextDocument.Text)
	return nil, nil
}

func (s *server) onDidChange(ctx context.Context, params protocol.DidChangeTextDocumentParams) (any, error) {
	if len(params.ContentChanges) == 0 {
		return nil, nil
	}
	// Full sync: the last change carries the whole buffer.
	text := params.ContentChanges[len(params.ContentChanges)-1].Text
	s.storeDoc(params.TextDocument.URI, text)
	s.publishDiagnostics(ctx, params.TextDocument.URI, text)
	return nil, nil
}

func (s *server) onDidSave(ctx context.Context, params protocol.DidSaveTextDocumentParams) (any, error) {
	// A save without text re-checks the open buffer.
	text := params.Text
	if text == "" {
		cached, ok := s.snapshot(params.TextDocument.URI)
		if !ok {
			return nil, nil
		}
		text = cached
	} else {
		s.storeDoc(params.TextDocument.URI, text)
	}
	s.publishDiagnostics(ctx, params.TextDocument.URI, text)
	return nil, nil
}

func (s *server) onDidClose(ctx context.Context, params protocol.DidCloseTextDocumentParams) (any, error) {
	s.mu.Lock()
	delete(s.docs, params.TextDocument.URI)
	s.mu.Unlock()
	// An empty list clears the closed file's diagnostics.
	s.publish(ctx, params.TextDocument.URI, []protocol.Diagnostic{})
	return nil, nil
}

// onInitialized asks the client to watch the design files and the manifest; a
// refusal is ignored. The call runs in a goroutine: the jsonrpc2 read loop is
// single-threaded, so waiting for the reply in the handler would deadlock.
func (s *server) onInitialized(ctx context.Context) {
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
// and every manifest published before, after a watched file changes on disk.
func (s *server) onDidChangeWatchedFiles(ctx context.Context) {
	// One publishDiagnostics per design root covers every open file under it.
	seenRoots := map[string]bool{}
	for u, src := range s.openDocs() {
		if _, root := designopts.ProjectOf(uriToPath(string(u))); root != "" {
			if seenRoots[root] {
				continue
			}
			seenRoots[root] = true
		}
		s.publishDiagnostics(ctx, u, src)
	}
	for _, m := range s.publishedManifests() {
		if !seenRoots[filepath.Dir(m)] {
			s.publishManifest(ctx, m)
		}
	}
}

// storeDoc records text as the open content of u.
func (s *server) storeDoc(u uri.URI, text string) {
	s.mu.Lock()
	s.docs[u] = text
	s.mu.Unlock()
}

// publishDiagnostics analyses the project of u holding src and publishes the
// diagnostics of u, of the other open files under its design root and of the
// root's manifest, or, outside a project, of the manifests published above u.
func (s *server) publishDiagnostics(ctx context.Context, u uri.URI, src string) {
	perFile, designRoot := s.buildProjectDiagnostics(u, src)
	path := uriToPath(string(u))
	s.publish(ctx, u, diagsFor(perFile, path))
	pushed := map[string]bool{path: true}
	for openURI := range s.openDocs() {
		op := uriToPath(string(openURI))
		if op == "" || pushed[op] || !isUnderDesignRoot(op, designRoot) {
			continue
		}
		pushed[op] = true
		s.publish(ctx, openURI, diagsFor(perFile, op))
	}
	if designRoot != "" {
		s.publishManifest(ctx, manifestPath(designRoot))
		return
	}
	for _, m := range s.publishedManifests() {
		if isUnderDesignRoot(path, filepath.Dir(m)) {
			s.publishManifest(ctx, m)
		}
	}
}

// publishManifest publishes the diagnostics of the manifest at path, and
// forgets it once it is gone.
func (s *server) publishManifest(ctx context.Context, path string) {
	diags, ok := manifestDiagnostics(path)
	s.mu.Lock()
	if s.manifests == nil {
		s.manifests = map[string]bool{}
	}
	if ok {
		s.manifests[path] = true
	} else {
		delete(s.manifests, path)
	}
	s.mu.Unlock()
	s.publish(ctx, uri.File(path), diags)
}

// publishedManifests returns the manifests whose diagnostics were published,
// sorted.
func (s *server) publishedManifests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Sorted(maps.Keys(s.manifests))
}

// publish sends the diagnostics of u to the client.
func (s *server) publish(ctx context.Context, u uri.URI, diags []protocol.Diagnostic) {
	_ = s.conn.Notify(ctx, protocol.MethodTextDocumentPublishDiagnostics, &protocol.PublishDiagnosticsParams{
		URI:         u,
		Diagnostics: diags,
	})
}

// diagsFor returns the diagnostics of key, never nil: clients ignore a null
// list but clear a file on `[]`.
func diagsFor(perFile map[string][]protocol.Diagnostic, key string) []protocol.Diagnostic {
	if d := perFile[key]; d != nil {
		return d
	}
	return []protocol.Diagnostic{}
}

// openDocs returns the text of each open document by URI.
func (s *server) openDocs() map[uri.URI]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.docs)
}

// openFiles returns the text of each open document by file path, whatever
// the escaping of its URI; a buffer with no file is left out.
func (s *server) openFiles() map[string]string {
	out := map[string]string{}
	for u, text := range s.openDocs() {
		if p := uriToPath(string(u)); p != "" {
			out[p] = text
		}
	}
	return out
}
