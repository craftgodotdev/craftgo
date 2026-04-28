package server

import (
	"encoding/binary"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestJSONArrayStreamFraming covers the array-stream lifecycle: opening
// bracket on construction, comma-separated payloads via Send, and the
// closing bracket via Close. The recorder also asserts Content-Type so a
// regression in NewJSONArrayStream's header set surfaces immediately.
func TestJSONArrayStreamFraming(t *testing.T) {
	rec := httptest.NewRecorder()
	s, err := NewJSONArrayStream(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(map[string]int{"b": 2}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	got := rec.Body.String()
	want := `[{"a":1},{"b":2}]`
	if got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if ct := rec.Result().Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
}

// TestJSONArrayStreamCloseIdempotent ensures a deferred Close paired with
// an explicit Close in a happy-path return doesn't double-emit `]`.
func TestJSONArrayStreamCloseIdempotent(t *testing.T) {
	rec := httptest.NewRecorder()
	s, _ := NewJSONArrayStream(rec)
	_ = s.Send(1)
	_ = s.Close()
	_ = s.Close()
	if got := rec.Body.String(); got != "[1]" {
		t.Errorf("body = %q", got)
	}
}

// TestCSVStreamHeaderAndEscape exercises both the lazy-header behaviour
// and RFC 4180 escaping (commas, quotes, embedded newline).
func TestCSVStreamHeaderAndEscape(t *testing.T) {
	rec := httptest.NewRecorder()
	s, err := NewCSVStream(rec)
	if err != nil {
		t.Fatal(err)
	}
	s.SetHeader([]string{"name", "note"})
	_ = s.Send([]string{"alice", "hi, friend"})
	_ = s.Send([]string{"bob", `with "quotes"`})
	_ = s.Send([]string{"carol", "two\nlines"})
	got := rec.Body.String()
	want := "name,note\n" +
		"alice,\"hi, friend\"\n" +
		"bob,\"with \"\"quotes\"\"\"\n" +
		"carol,\"two\nlines\"\n"
	if got != want {
		t.Errorf("body = %q\nwant %q", got, want)
	}
	if ct := rec.Result().Header.Get("Content-Type"); ct != "text/csv" {
		t.Errorf("content-type = %q", ct)
	}
}

// TestCSVStreamSkipsHeader verifies that omitting SetHeader results in a
// header-less stream — useful for appending to an existing file.
func TestCSVStreamSkipsHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	s, _ := NewCSVStream(rec)
	_ = s.Send([]string{"x"})
	if got := rec.Body.String(); got != "x\n" {
		t.Errorf("body = %q", got)
	}
}

// TestConcatStreamConcatenates checks that ConcatStream emits payloads
// back-to-back with no separator, matching the runtime contract for
// `@format(concat)` consumers.
func TestConcatStreamConcatenates(t *testing.T) {
	rec := httptest.NewRecorder()
	s, _ := NewConcatStream(rec)
	_ = s.Send(1)
	_ = s.Send("two")
	_ = s.Send(map[string]int{"k": 3})
	if got := rec.Body.String(); got != `1"two"{"k":3}` {
		t.Errorf("body = %q", got)
	}
}

// TestLengthPrefixedStreamFrames validates the big-endian uint32 length
// header that precedes each JSON payload — without it, framing parsers
// can't find object boundaries.
func TestLengthPrefixedStreamFrames(t *testing.T) {
	rec := httptest.NewRecorder()
	s, _ := NewLengthPrefixedStream(rec)
	_ = s.Send(map[string]int{"a": 1})
	_ = s.Send("hi")
	body := rec.Body.Bytes()

	// First frame: length + JSON payload.
	if len(body) < 4 {
		t.Fatalf("body too short: %d", len(body))
	}
	n := binary.BigEndian.Uint32(body[:4])
	first := string(body[4 : 4+n])
	if first != `{"a":1}` {
		t.Errorf("first payload = %q", first)
	}

	// Second frame.
	rest := body[4+n:]
	n2 := binary.BigEndian.Uint32(rest[:4])
	second := string(rest[4 : 4+n2])
	if second != `"hi"` {
		t.Errorf("second payload = %q", second)
	}
	if !strings.Contains(rec.Result().Header.Get("Content-Type"), "octet-stream") {
		t.Errorf("content-type = %q", rec.Result().Header.Get("Content-Type"))
	}
}

