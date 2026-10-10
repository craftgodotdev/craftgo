package server

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/log"
)

// A handler behind Recovery, AccessLog and Compress sees the writer as net/http's: it can hijack
// the connection, as a websocket upgrade does, flush, and copy a file in with ReadFrom.
func TestWritersKeepTheConnectionInterfaces(t *testing.T) {
	s := New(nil, WithoutDefaultHealth())
	s.Use(AccessLog(log.Discard()))
	s.Use(Compress())
	s.HandleFunc("GET /ws", func(w http.ResponseWriter, _ *http.Request) {
		_, flusher := w.(http.Flusher)
		_, readerFrom := w.(io.ReaderFrom)
		h, ok := w.(http.Hijacker)
		if !ok || !flusher || !readerFrom {
			t.Errorf("hijacker %v, flusher %v, readerFrom %v", ok, flusher, readerFrom)
			return
		}
		c, rw, err := h.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		defer c.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: x\r\nConnection: Upgrade\r\n\r\nhello")
		_ = rw.Flush()
	})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	c, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, _ = io.WriteString(c, "GET /ws HTTP/1.1\r\nHost: x\r\nAccept-Encoding: gzip\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("status %d, want the hijacked connection's 101", resp.StatusCode)
	}
}

// A file served through ReadFrom arrives whole, plain for a type Compress skips and gzipped for
// one it encodes.
func TestWritersReadFrom(t *testing.T) {
	dir := t.TempDir()
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 4096)...)
	text := strings.Repeat("craftgo ", 1024)
	if err := os.WriteFile(filepath.Join(dir, "a.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(nil, WithoutDefaultHealth())
	s.Use(Compress())
	s.Handle("GET /f/", http.StripPrefix("/f/", http.FileServer(http.Dir(dir))))
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	for name, want := range map[string]string{"a.png": string(png), "a.txt": text} {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/f/"+name, nil)
		req.Header.Set("Accept-Encoding", "gzip")
		resp, err := (&http.Transport{DisableCompression: true}).RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		got := string(raw)
		if resp.Header.Get("Content-Encoding") == "gzip" {
			got = gunzip(t, raw)
		}
		if got != want || (name == "a.png") == (resp.Header.Get("Content-Encoding") == "gzip") {
			t.Errorf("%s: %d bytes, Content-Encoding %q", name, len(got), resp.Header.Get("Content-Encoding"))
		}
	}
}

// A wrapper over a writer that cannot hijack fails to, as the writer would.
func TestWritersHijackUnsupported(t *testing.T) {
	for _, w := range []http.Hijacker{&trackingWriter{ResponseWriter: httptest.NewRecorder()}, &compressWriter{ResponseWriter: httptest.NewRecorder()}} {
		if _, _, err := w.Hijack(); !errors.Is(err, http.ErrNotSupported) {
			t.Errorf("%T: got %v, want http.ErrNotSupported", w, err)
		}
	}
}
