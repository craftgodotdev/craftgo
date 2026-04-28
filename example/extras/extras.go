// Package extras hosts hand-written routes for HTTP modes the v1 codegen
// does not yet emit. Today: multipart file upload and Server-Sent
// Events. When the codegen lands `@stream @format(sse)` and `file`
// binding, these routes can move into the DSL — but until then this
// package shows the pattern: the same Server, the same ServiceContext,
// the same middleware machinery, just registered manually.
package extras

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/dropship-dev/craftgo/pkg/server"

	"github.com/dropship-dev/craftgo/example/svccontext"
)

// RegisterExtras attaches every handcrafted route onto srv. Wire it
// from main.go alongside `routes.RegisterAll(srv, svc)`. Covers four
// HTTP modes that v1 codegen does not yet emit:
//
//   - POST /upload         — multipart/form-data upload (`file` field).
//   - GET  /events         — Server-Sent Events stream (text/event-stream).
//   - GET  /feed           — NDJSON stream (application/x-ndjson).
//   - POST /raw, GET /raw  — raw bytes in and out (application/octet-stream).
func RegisterExtras(srv *server.Server, svc *svccontext.ServiceContext) {
	srv.HandleFunc("POST /api/v1/upload", uploadHandler(svc))
	srv.HandleFunc("GET /api/v1/events", eventsHandler(svc))
	srv.HandleFunc("GET /api/v1/feed", ndjsonHandler(svc))
	srv.HandleFunc("POST /api/v1/raw", rawIngestHandler(svc))
	srv.HandleFunc("GET /api/v1/raw", rawDownloadHandler(svc))
}

// uploadHandler parses a multipart/form-data request, reads the
// `file` form field, and replies with the file's name + size. A real
// project would write the bytes to object storage from here.
func uploadHandler(_ *svccontext.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 10 MiB cap. Clients hitting that limit get a 413 from the
		// stdlib parser — propagate to the client.
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer file.Close()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"filename": header.Filename,
			"size":     header.Size,
			"received": true,
		})
	}
}

// eventsHandler streams 5 server-sent events (one per second) and
// closes the connection. Demonstrates the SSE wire format: each event
// is `data: <json>\n\n`. Clients consume via EventSource(...).
func eventsHandler(_ *svccontext.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for i := 0; i < 5; i++ {
			select {
			case <-r.Context().Done():
				return
			case t := <-ticker.C:
				fmt.Fprintf(w, "data: {\"tick\": %d, \"at\": \"%s\"}\n\n", i+1, t.Format(time.RFC3339))
				flusher.Flush()
			}
		}
	}
}

// ndjsonHandler streams newline-delimited JSON (application/x-ndjson)
// — a common format for log feeds and event streams that need to be
// line-parsed by the consumer. Each line is a complete JSON object;
// no surrounding array.
func ndjsonHandler(_ *svccontext.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		enc := json.NewEncoder(w)
		for i := 0; i < 5; i++ {
			select {
			case <-r.Context().Done():
				return
			default:
			}
			if err := enc.Encode(map[string]any{"seq": i, "ts": time.Now().UnixNano()}); err != nil {
				return
			}
			flusher.Flush()
			time.Sleep(200 * time.Millisecond)
		}
	}
}

// rawIngestHandler accepts an opaque byte stream — application/octet-
// stream is the canonical content type, but anything passes through.
// Replies with the consumed byte count. A real ingester would write
// straight to the destination via io.Copy.
func rawIngestHandler(_ *svccontext.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int64{"bytes": n})
	}
}

// rawDownloadHandler emits a raw byte payload — demonstrates the
// `writer` builtin's role: the handler writes straight to w without
// JSON envelope. Real-world example: streaming a file from object
// storage back to the client.
func rawDownloadHandler(_ *svccontext.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		// Stream a fixed sample payload. Replace with io.Copy from
		// the storage tier in production.
		_, _ = w.Write([]byte("the quick brown fox jumps over the lazy dog\n"))
	}
}
