package server

import (
	"bytes"
	"compress/flate"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"strings"
	"testing"
)

// largeBody returns a payload above the default MinSize.
func largeBody() []byte {
	return bytes.Repeat([]byte("hello-craftgo-"), 200) // 2800 bytes
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
		// The third write crosses MinSize.
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

// A Flush before any write sends a 200 head uncompressed, and the stream after it.
func TestCompressFlushBeforeWrite(t *testing.T) {
	h := Compress()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte("data: hi\n\n"))
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !rec.Flushed {
		t.Fatalf("status = %d, flushed = %v, want a flushed 200", rec.Code, rec.Flushed)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want none after an early flush", got)
	}
	if got := rec.Body.String(); got != "data: hi\n\n" {
		t.Errorf("body = %q", got)
	}
}

// An informational status goes out at once under Compress, and the final status written after
// it reaches the client.
func TestCompressSendsAnInformationalStatusAtOnce(t *testing.T) {
	observeLogs(t)
	s := New(nil)
	s.Use(Compress())
	s.HandleFunc("GET /created", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Link", "</app.css>; rel=preload")
		w.WriteHeader(http.StatusEarlyHints)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(largeBody())
	})
	s.HandleFunc("GET /failed", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusEarlyHints)
		WriteError(w, r, errors.New("boom"))
	})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	for _, tc := range []struct {
		path     string
		status   int
		encoding string
	}{
		{"/created", http.StatusCreated, "gzip"},
		{"/failed", http.StatusInternalServerError, ""},
	} {
		hints := 0
		ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{
			Got1xxResponse: func(code int, _ textproto.MIMEHeader) error {
				if code == http.StatusEarlyHints {
					hints++
				}
				return nil
			},
		})
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+tc.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Accept-Encoding", "gzip")
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", tc.path, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if got := resp.Header.Get("Content-Encoding"); resp.StatusCode != tc.status || got != tc.encoding || hints != 1 {
			t.Errorf("GET %s: %d, encoding %q, after %d early hints; want %d, %q, after 1",
				tc.path, resp.StatusCode, got, hints, tc.status, tc.encoding)
		}
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
		// The first listed wins.
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

// A q=0 coding is refused (RFC 7231 §5.3.1).
func TestNegotiateEncodingHonorsQZero(t *testing.T) {
	cases := map[string]string{
		"gzip;q=0":                  "",
		"gzip; q=0.0":               "",
		"gzip;q=0, deflate":         "deflate",
		"gzip;q=0,deflate;q=0":      "",
		"gzip":                      "gzip",
		"gzip;q=1.0":                "gzip",
		"deflate;q=0.5, gzip;q=0.9": "deflate", // first non-zero wins
		"":                          "",
	}
	for in, want := range cases {
		if got := negotiateEncoding(in); got != want {
			t.Errorf("negotiateEncoding(%q) = %q, want %q", in, got, want)
		}
	}
}

// trackingWriter and compressWriter unwrap to the writer they wrap.
func TestResponseWritersUnwrap(t *testing.T) {
	base := httptest.NewRecorder()
	var tw http.ResponseWriter = &trackingWriter{ResponseWriter: base}
	if u, ok := tw.(interface{ Unwrap() http.ResponseWriter }); !ok || u.Unwrap() != base {
		t.Errorf("trackingWriter.Unwrap must return the wrapped writer")
	}
	var cw http.ResponseWriter = &compressWriter{ResponseWriter: base}
	if u, ok := cw.(interface{ Unwrap() http.ResponseWriter }); !ok || u.Unwrap() != base {
		t.Errorf("compressWriter.Unwrap must return the wrapped writer")
	}
}
