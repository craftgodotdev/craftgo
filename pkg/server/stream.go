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

// JSONArrayStream emits a single JSON array (`application/json`) where
// each [JSONArrayStream.Send] appends one element. The opening `[` is
// written on construction; subsequent items are separated with commas; the
// closing `]` is emitted by [JSONArrayStream.Close]. Generated handlers
// invoke Close via `defer` so partial responses still produce valid JSON.
type JSONArrayStream struct {
	w       http.ResponseWriter
	flusher http.Flusher
	count   int
	closed  bool
}

// NewJSONArrayStream wires the JSON content type onto w and writes the
// opening `[`. Errors when the writer is not a Flusher.
func NewJSONArrayStream(w http.ResponseWriter) (*JSONArrayStream, error) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, errNoFlusher
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write([]byte("[")); err != nil {
		return nil, err
	}
	return &JSONArrayStream{w: w, flusher: f}, nil
}

// Send marshals v and writes it as the next element of the array,
// emitting a leading comma when this is not the first element. The
// flush happens after the write so consumers can stream-parse.
func (s *JSONArrayStream) Send(v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if s.count > 0 {
		if _, err := s.w.Write([]byte(",")); err != nil {
			return err
		}
	}
	if _, err := s.w.Write(body); err != nil {
		return err
	}
	s.count++
	s.flusher.Flush()
	return nil
}

// Close emits the closing `]` and flushes. Idempotent — repeat calls are
// no-ops so logic code can defer it without coordinating with the runtime.
func (s *JSONArrayStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if _, err := s.w.Write([]byte("]")); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

// CSVStream is the runtime helper for `@stream @format(csv)` — emits one
// CSV row per [CSVStream.Send]. The first call writes the header derived
// from the field-name argument; subsequent calls write the values supplied
// as a `[]string`. Logic code is responsible for preserving column order.
type CSVStream struct {
	w       http.ResponseWriter
	flusher http.Flusher
	header  []string
	wrote   bool
}

// NewCSVStream sets the `text/csv` content type on w. The optional column
// header is supplied via [CSVStream.SetHeader] before the first Send.
func NewCSVStream(w http.ResponseWriter) (*CSVStream, error) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, errNoFlusher
	}
	w.Header().Set("Content-Type", "text/csv")
	return &CSVStream{w: w, flusher: f}, nil
}

// SetHeader stores the column names that will be emitted as the first row
// the next time Send is invoked. Calling SetHeader after the first Send is
// a no-op — the header has already been committed (or skipped) at that
// point.
func (s *CSVStream) SetHeader(header []string) { s.header = header }

// Send writes one CSV row. Embedded commas, quotes, and newlines are
// escaped per RFC 4180. Header emission happens lazily on the first Send
// so the connection isn't committed until logic actually has data.
func (s *CSVStream) Send(row []string) error {
	if !s.wrote && len(s.header) > 0 {
		if _, err := s.w.Write([]byte(csvJoin(s.header) + "\n")); err != nil {
			return err
		}
		s.wrote = true
	}
	if _, err := s.w.Write([]byte(csvJoin(row) + "\n")); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

// csvJoin produces an RFC 4180-compliant comma-joined row. Cells that
// contain a comma, double-quote, CR, or LF are wrapped in double quotes
// with embedded `"` doubled.
func csvJoin(cells []string) string {
	out := make([]byte, 0, 32)
	for i, cell := range cells {
		if i > 0 {
			out = append(out, ',')
		}
		needQuote := false
		for _, r := range cell {
			if r == ',' || r == '"' || r == '\n' || r == '\r' {
				needQuote = true
				break
			}
		}
		if !needQuote {
			out = append(out, cell...)
			continue
		}
		out = append(out, '"')
		for _, r := range cell {
			if r == '"' {
				out = append(out, '"', '"')
			} else {
				out = append(out, []byte(string(r))...)
			}
		}
		out = append(out, '"')
	}
	return string(out)
}

// ConcatStream is the runtime helper for `@format(concat)`: every Send
// writes the JSON-encoded value with no separator between events. Useful
// for clients that consume `application/json-seq`-style streams or simple
// "marshal-and-flush" feeds. No framing is added — the consumer is
// responsible for object-boundary detection.
type ConcatStream struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

// NewConcatStream sets `application/json` and returns the stream.
func NewConcatStream(w http.ResponseWriter) (*ConcatStream, error) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, errNoFlusher
	}
	w.Header().Set("Content-Type", "application/json")
	return &ConcatStream{w: w, flusher: f}, nil
}

// Send marshals v and writes it back-to-back with prior events.
func (s *ConcatStream) Send(v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := s.w.Write(body); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

// LengthPrefixedStream emits each event as `<uint32-be length>` followed
// by the JSON-encoded payload, suitable for binary protocols where the
// consumer knows the byte boundary in advance. Content type defaults to
// `application/octet-stream`.
type LengthPrefixedStream struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

// NewLengthPrefixedStream sets `application/octet-stream` on w and
// returns the stream.
func NewLengthPrefixedStream(w http.ResponseWriter) (*LengthPrefixedStream, error) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, errNoFlusher
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	return &LengthPrefixedStream{w: w, flusher: f}, nil
}

// Send writes the big-endian uint32 length of the JSON payload followed
// by the payload bytes. Lengths exceeding 2^32-1 are rejected.
func (s *LengthPrefixedStream) Send(v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	n := uint32(len(body))
	hdr := []byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}
	if _, err := s.w.Write(hdr); err != nil {
		return err
	}
	if _, err := s.w.Write(body); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}
