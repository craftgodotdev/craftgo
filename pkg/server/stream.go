package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// errNoFlusher is returned by stream constructors when the underlying
// http.ResponseWriter does not satisfy http.Flusher. Generated stream
// handlers translate this into a 500 response before invoking logic.
var errNoFlusher = errors.New("streaming unsupported: ResponseWriter is not a Flusher")

// SSEStream is the runtime helper bound to a Server-Sent Events
// connection. The generated stream handler constructs one per request
// and passes it to the logic layer; logic emits typed payloads via
// [SSEStream.Send] and the framing is handled here.
type SSEStream struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

// NewSSEStream wires the SSE response headers (`text/event-stream`,
// no-cache, keep-alive) onto w and returns a stream the logic can
// publish to. Returns an error when the writer does not support
// flushing — the caller is expected to translate that into a 500.
func NewSSEStream(w http.ResponseWriter) (*SSEStream, error) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, errNoFlusher
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	return &SSEStream{w: w, flusher: f}, nil
}

// Send marshals v as JSON and emits one SSE `data: ...` frame
// followed by the protocol's two-newline terminator. The flush
// happens before Send returns so subscribers see events in real time.
func (s *SSEStream) Send(v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "data: %s\n\n", body); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

// SendNamed emits a named event (`event: <name>\ndata: <json>\n\n`)
// for clients using EventSource's `addEventListener("name", ...)`.
func (s *SSEStream) SendNamed(name string, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, body); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

// NDJSONStream is the runtime helper for newline-delimited JSON
// streams (`application/x-ndjson`). Each [NDJSONStream.Send] call
// emits one complete JSON object followed by `\n`, then flushes.
type NDJSONStream struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

// NewNDJSONStream sets the NDJSON content type on w and returns a
// stream. Same Flusher contract as [NewSSEStream].
func NewNDJSONStream(w http.ResponseWriter) (*NDJSONStream, error) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, errNoFlusher
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	return &NDJSONStream{w: w, flusher: f}, nil
}

// Send marshals v as JSON, appends `\n`, and flushes. Each Send
// produces exactly one logical record.
func (s *NDJSONStream) Send(v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := s.w.Write(append(body, '\n')); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

// RawStream is the runtime helper for raw-bytes streaming responses
// (`@raw @stream` in the DSL). It wraps an http.ResponseWriter so
// logic code can `Write` arbitrary bytes and have each chunk flushed
// automatically — typical use cases are binary protocol upgrades and
// long-running transcodes that emit progress chunks.
type RawStream struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

// NewRawStream sets the content type on w (defaulting to
// `application/octet-stream` when empty) and returns the stream.
// Errors when the writer is not a Flusher — same contract as the
// SSE / NDJSON constructors.
func NewRawStream(w http.ResponseWriter, contentType string) (*RawStream, error) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, errNoFlusher
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	return &RawStream{w: w, flusher: f}, nil
}

// Write satisfies io.Writer. Every call flushes after writing so the
// chunk reaches the client immediately.
func (s *RawStream) Write(b []byte) (int, error) {
	n, err := s.w.Write(b)
	s.flusher.Flush()
	return n, err
}

// Flush exposes the underlying flusher in case logic wants to flush
// without writing more bytes.
func (s *RawStream) Flush() { s.flusher.Flush() }
