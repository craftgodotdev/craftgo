package server

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAcceptsEncoding(t *testing.T) {
	cases := []struct {
		accept, coding string
		want           bool
	}{
		{"zstd", "zstd", true},
		{"gzip, zstd", "zstd", true},
		{"ZSTD", "zstd", true},
		{"zstd", "ZSTD", true},
		{"zstd;q=0.5", "zstd", true},
		{"gzip;q=0, zstd", "zstd", true},
		{"zstd;q=0", "zstd", false},
		{"gzip, zstd;q=0.0", "zstd", false},
		{"gzip", "zstd", false},
		{"", "zstd", false},
		{"*", "zstd", false}, // no wildcard semantics, same as Compress
		{"zstd", "", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if c.accept != "" {
			r.Header.Set("Accept-Encoding", c.accept)
		}
		if got := AcceptsEncoding(r, c.coding); got != c.want {
			t.Errorf("AcceptsEncoding(%q, %q) = %v, want %v", c.accept, c.coding, got, c.want)
		}
	}
}

func TestWriteBytes(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := WriteBytes(rec, http.StatusAccepted, "text/plain", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("Content-Length"); got != "5" {
		t.Errorf("Content-Length = %q, want 5", got)
	}
	if rec.Body.String() != "hello" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestWriteBytesKeepsCallerContentType(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Type", "application/octet-stream")
	if err := WriteBytes(rec, http.StatusOK, "", []byte{1, 2}); err != nil {
		t.Fatal(err)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("empty contentType must leave the caller's header alone, got %q", got)
	}
}

const ctJSON = "application/json; charset=utf-8"

var (
	precompressedBody = []byte("zstd-bytes")
	decodedBody       = []byte(`{"ok":true}`)
)

func fakeDecode(b []byte) ([]byte, error) {
	if !bytes.Equal(b, precompressedBody) {
		return nil, errors.New("unexpected input")
	}
	return decodedBody, nil
}

func TestWritePrecompressedAcceptedGoesVerbatim(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept-Encoding", "gzip, zstd")
	if err := WritePrecompressed(rec, r, http.StatusOK, ctJSON, "zstd", precompressedBody, fakeDecode); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rec.Body.Bytes(), precompressedBody) {
		t.Errorf("body must be the stored bytes verbatim, got %q", rec.Body.Bytes())
	}
	h := rec.Header()
	if h.Get("Content-Encoding") != "zstd" {
		t.Errorf("Content-Encoding = %q, want zstd", h.Get("Content-Encoding"))
	}
	if h.Get("Content-Type") != ctJSON {
		t.Errorf("Content-Type = %q", h.Get("Content-Type"))
	}
	if h.Get("Content-Length") != "10" {
		t.Errorf("Content-Length = %q, want 10", h.Get("Content-Length"))
	}
	if h.Get("Vary") != "Accept-Encoding" {
		t.Errorf("Vary = %q, want Accept-Encoding", h.Get("Vary"))
	}
}

func TestWritePrecompressedNotAcceptedDecodes(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	if err := WritePrecompressed(rec, r, http.StatusOK, ctJSON, "zstd", precompressedBody, fakeDecode); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rec.Body.Bytes(), decodedBody) {
		t.Errorf("body must be the decoded bytes, got %q", rec.Body.Bytes())
	}
	h := rec.Header()
	if h.Get("Content-Encoding") != "" {
		t.Errorf("decoded path must not set Content-Encoding, got %q", h.Get("Content-Encoding"))
	}
	if h.Get("Content-Length") != "11" {
		t.Errorf("Content-Length = %q, want 11", h.Get("Content-Length"))
	}
	if h.Get("Vary") != "Accept-Encoding" {
		t.Errorf("Vary = %q, want Accept-Encoding", h.Get("Vary"))
	}
}

func TestWritePrecompressedRefusedCodingDecodes(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept-Encoding", "zstd;q=0, gzip")
	if err := WritePrecompressed(rec, r, http.StatusOK, ctJSON, "zstd", precompressedBody, fakeDecode); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rec.Body.Bytes(), decodedBody) || rec.Header().Get("Content-Encoding") != "" {
		t.Errorf("q=0 is a refusal: want decoded body without Content-Encoding, got %q / %q", rec.Body.Bytes(), rec.Header().Get("Content-Encoding"))
	}
}

func TestWritePrecompressedNoDecoderWritesNothing(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	err := WritePrecompressed(rec, r, http.StatusOK, ctJSON, "zstd", precompressedBody, nil)
	if !errors.Is(err, ErrNoDecoder) {
		t.Fatalf("err = %v, want ErrNoDecoder", err)
	}
	if rec.Body.Len() != 0 || rec.Header().Get("Content-Encoding") != "" || rec.Header().Get("Content-Length") != "" {
		t.Errorf("nothing must be written on ErrNoDecoder: body=%q headers=%v", rec.Body.Bytes(), rec.Header())
	}
}

func TestWritePrecompressedDecodeErrorWritesNothing(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	boom := errors.New("corrupt cache entry")
	err := WritePrecompressed(rec, r, http.StatusOK, ctJSON, "zstd", precompressedBody, func([]byte) ([]byte, error) { return nil, boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the decoder's error", err)
	}
	if rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != "" {
		t.Errorf("nothing must be written on a decode error: body=%q", rec.Body.Bytes())
	}
}

func TestWritePrecompressedDoesNotStackVary(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.Header().Add("Vary", "Accept-Encoding")
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept-Encoding", "zstd")
	if err := WritePrecompressed(rec, r, http.StatusOK, ctJSON, "zstd", precompressedBody, nil); err != nil {
		t.Fatal(err)
	}
	if got := rec.Header().Values("Vary"); len(got) != 1 {
		t.Errorf("Vary must not be duplicated, got %v", got)
	}
}

// A pre-gzipped body served through the Compress middleware must reach the
// client exactly once encoded: the middleware sees Content-Encoding and
// passes the bytes through untouched.
func TestWritePrecompressedThroughCompressIsNotDoubleEncoded(t *testing.T) {
	plain := []byte(strings.Repeat(`{"item":"payload"},`, 200))
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(plain)
	_ = zw.Close()
	stored := gz.Bytes()

	s := newTestServer(t)
	s.Use(Compress())
	s.HandleFunc("GET /cached", func(w http.ResponseWriter, r *http.Request) {
		if err := WritePrecompressed(w, r, http.StatusOK, ctJSON, "gzip", stored, func(b []byte) ([]byte, error) {
			zr, err := gzip.NewReader(bytes.NewReader(b))
			if err != nil {
				return nil, err
			}
			return io.ReadAll(zr)
		}); err != nil {
			t.Errorf("WritePrecompressed: %v", err)
		}
	})
	h := finalize(s)

	// Client accepts gzip: stored bytes go out verbatim, one gzip layer.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/cached", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", rec.Header().Get("Content-Encoding"))
	}
	if !bytes.Equal(rec.Body.Bytes(), stored) {
		t.Errorf("body must be the stored gzip bytes verbatim (not re-encoded)")
	}
	zr, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(zr)
	if !bytes.Equal(got, plain) {
		t.Errorf("one gunzip must yield the plain payload")
	}

	// Client accepts nothing: decoded path, identity bytes, no encoding.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/cached", nil))
	if rec.Header().Get("Content-Encoding") != "" {
		t.Errorf("identity client must get no Content-Encoding, got %q", rec.Header().Get("Content-Encoding"))
	}
	if !bytes.Equal(rec.Body.Bytes(), plain) {
		t.Errorf("identity client must get the decoded payload")
	}
}

// A raw-response handler that already wrote part of a response and then
// returns an error must not have an error envelope spliced into the body,
// whatever writers sit between it and the Recovery wrapper.
func TestWriteErrorAfterCommitThroughWrappers(t *testing.T) {
	for _, tc := range []struct {
		name         string
		useCompress  bool
		acceptGzip   bool
		partialBytes int
	}{
		{"recovery only", false, false, 32},
		{"compress buffering below threshold", true, true, 32},
		{"compress committed above threshold", true, true, 4096},
		{"compress bypassed (no accept)", true, false, 32},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := observeLogs(t)
			partial := strings.Repeat("x", tc.partialBytes)
			s := newTestServer(t)
			s.Use(AccessLog(s.Logger()))
			if tc.useCompress {
				s.Use(Compress())
			}
			s.HandleFunc("GET /stream", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(partial))
				WriteError(w, r, fakeStatusError{msg: "late failure", status: http.StatusConflict})
			})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/stream", nil)
			if tc.acceptGzip {
				req.Header.Set("Accept-Encoding", "gzip")
			}
			finalize(s).ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want the committed 200", rec.Code)
			}
			body := rec.Body.Bytes()
			if rec.Header().Get("Content-Encoding") == "gzip" {
				zr, err := gzip.NewReader(bytes.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				body, _ = io.ReadAll(zr)
			}
			if string(body) != partial {
				t.Errorf("committed body must stay intact, got %q", body)
			}
			if strings.Contains(string(body), "late failure") {
				t.Errorf("error envelope leaked into the committed body")
			}
			found := false
			for _, e := range logs.All() {
				if strings.Contains(e.Message, "service error after response committed") {
					found = true
				}
			}
			if !found {
				t.Errorf("late error must be logged; got %d log entries", logs.Len())
			}
		})
	}
}
