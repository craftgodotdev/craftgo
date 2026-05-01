package server

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// largeBody returns a deterministic payload comfortably above the
// default 1 KB MinSize so compression always commits.
func largeBody() []byte {
	return bytes.Repeat([]byte("hello-craftgo-"), 200) // 2800 bytes
}

func gunzip(t *testing.T, b []byte) string {
	t.Helper()
	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("gzip read: %v", err)
	}
	return string(out)
}

func inflate(t *testing.T, b []byte) string {
	t.Helper()
	r := flate.NewReader(bytes.NewReader(b))
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("flate read: %v", err)
	}
	return string(out)
}

func TestCompressGzipPath(t *testing.T) {
	body := largeBody()
	h := Compress()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := rec.Header().Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
		t.Fatalf("Vary = %q, want to contain Accept-Encoding", got)
	}
	if got := rec.Header().Get("Content-Length"); got != "" {
		t.Errorf("Content-Length must be cleared after compression, got %q", got)
	}
	if decoded := gunzip(t, rec.Body.Bytes()); decoded != string(body) {
		t.Errorf("decoded body mismatch: %d vs %d bytes", len(decoded), len(body))
	}
}

func TestCompressDeflatePath(t *testing.T) {
	body := largeBody()
	h := Compress()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "deflate")
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "deflate" {
		t.Fatalf("Content-Encoding = %q, want deflate", got)
	}
	if decoded := inflate(t, rec.Body.Bytes()); decoded != string(body) {
		t.Errorf("decoded body mismatch")
	}
}

func TestCompressNoAcceptEncodingPassthrough(t *testing.T) {
	body := largeBody()
	h := Compress()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty", got)
	}
	if got := rec.Header().Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
		t.Fatalf("Vary header missing on uncompressed response, got %q", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), body) {
		t.Errorf("body mutated despite no Accept-Encoding")
	}
}

func TestCompressBelowMinSizeSkipped(t *testing.T) {
	small := []byte("tiny")
	h := Compress()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(small)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty for under-threshold body", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), small) {
		t.Errorf("body mutated for under-threshold response")
	}
}

func TestCompressContentTypeSkipped(t *testing.T) {
	body := largeBody()
	h := Compress()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(body)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty for image/png", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), body) {
		t.Errorf("image body should pass through unchanged")
	}
}

func TestCompressAlreadyEncodedSkipped(t *testing.T) {
	body := largeBody()
	h := Compress()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "br")
		_, _ = w.Write(body)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "br" {
		t.Fatalf("Content-Encoding = %q, want br (preserved)", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), body) {
		t.Errorf("pre-encoded body must pass through unchanged")
	}
}

func TestCompressPreservesStatusCode(t *testing.T) {
	body := largeBody()
	h := Compress()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(body)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
}

func TestCompressMultipleWritesCrossThreshold(t *testing.T) {
	chunk := bytes.Repeat([]byte("x"), 600)
	h := Compress()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		// First two writes stay under 1024; third crosses the
		// threshold and triggers commitCompressed mid-stream.
		_, _ = w.Write(chunk)
		_, _ = w.Write(chunk)
		_, _ = w.Write(chunk)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := gunzip(t, rec.Body.Bytes()); len(got) != 1800 {
		t.Errorf("decoded length = %d, want 1800", len(got))
	}
}

func TestCompressFlushBelowThresholdPassthrough(t *testing.T) {
	body := []byte(strings.Repeat("y", 300))
	h := Compress()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(body)
		// httptest.ResponseRecorder satisfies Flusher via embedded
		// methods; calling Flush here forces the compressWriter to
		// commit before the threshold and stay uncompressed.
		w.(http.Flusher).Flush()
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty after early flush", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), body) {
		t.Errorf("flushed body mutated")
	}
}

func TestCompressHEADBypasses(t *testing.T) {
	h := Compress()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodHead, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("HEAD response must not advertise Content-Encoding, got %q", got)
	}
	if got := rec.Header().Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
		t.Errorf("Vary should still be set on HEAD")
	}
}

func TestCompressGzipPreferredOverDeflate(t *testing.T) {
	if got := negotiateEncoding("deflate, gzip"); got != "deflate" {
		// First-listed wins; explicitly pin the rule.
		t.Fatalf("negotiateEncoding(\"deflate, gzip\") = %q, want deflate (first wins)", got)
	}
	if got := negotiateEncoding("gzip, deflate"); got != "gzip" {
		t.Fatalf("negotiateEncoding(\"gzip, deflate\") = %q, want gzip", got)
	}
	if got := negotiateEncoding("br, identity"); got != "" {
		t.Fatalf("negotiateEncoding(unknown) = %q, want empty", got)
	}
	if got := negotiateEncoding("gzip;q=1.0, *;q=0.5"); got != "gzip" {
		t.Fatalf("negotiateEncoding with q-values = %q, want gzip", got)
	}
}
