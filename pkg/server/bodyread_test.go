package server

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"go.uber.org/zap/zapcore"
)

// readFailure is the error a request body read returns when the connection fails: a timeout, or a
// reset, naming the socket's addresses.
func readFailure(timeout bool) error {
	cause := errors.New("connection reset by peer")
	if timeout {
		cause = os.ErrDeadlineExceeded
	}
	return &net.OpError{Op: "read", Net: "tcp",
		Source: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080},
		Addr:   &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 50123}, Err: cause}
}

// A body the connection fails to deliver while the request binds is answered 408 for a timeout and
// 400 otherwise, with a generic message; the detail and its addresses go to the log at Warn only.
func TestWriteValidationErrorBodyReadFailure(t *testing.T) {
	for _, c := range []struct {
		timeout bool
		status  int
		message string
	}{
		{true, http.StatusRequestTimeout, "request timeout"},
		{false, http.StatusBadRequest, "bad request"},
	} {
		logs := observeLogs(t)
		rec := httptest.NewRecorder()
		WriteValidationError(rec, httptest.NewRequest(http.MethodPost, "/", nil), fmt.Errorf("decode: %w", readFailure(c.timeout)))
		if rec.Code != c.status || rec.Body.String() != `{"message":"`+c.message+`"}`+"\n" {
			t.Errorf("timeout %v: got %d %q", c.timeout, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "127.0.0.1") {
			t.Errorf("the body names a socket address: %q", rec.Body.String())
		}
		if warn := logs.FilterLevelExact(zapcore.WarnLevel).FilterMessage("request body read failed"); warn.Len() != 1 {
			t.Errorf("timeout %v: %d Warn lines, want the detail logged once", c.timeout, warn.Len())
		}
	}
}

// Logic that hands back the failure of a read of its request's body, a raw request's, is answered
// as a binding is; a network error of its own, a dependency's, stays an unhandled error.
func TestWriteErrorBodyReadFailure(t *testing.T) {
	h := func(own error) http.Handler {
		return WithLimits(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, err := io.ReadAll(r.Body)
			if own != nil {
				err = own
			}
			WriteError(w, r, fmt.Errorf("ingest: %w", err))
		}), Limits{MaxBodySize: 1 << 20})
	}
	for _, c := range []struct {
		name   string
		body   io.Reader
		own    error
		status int
	}{
		{"timeout", iotest.ErrReader(readFailure(true)), nil, http.StatusRequestTimeout},
		{"cut short", iotest.ErrReader(io.ErrUnexpectedEOF), nil, http.StatusBadRequest},
		{"a dependency's", strings.NewReader("{}"), readFailure(true), http.StatusInternalServerError},
	} {
		observeLogs(t)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/", io.NopCloser(c.body))
		req.ContentLength = -1
		h(c.own).ServeHTTP(rec, req)
		if rec.Code != c.status {
			t.Errorf("%s: got %d %q, want %d", c.name, rec.Code, rec.Body.String(), c.status)
		}
	}
}

// A slow client cut off by the read timeout gets 408 and a generic message, not the socket's
// addresses.
func TestReadTimeoutAnswers408(t *testing.T) {
	observeLogs(t)
	s := New(nil, WithoutDefaultHealth())
	s.SetDefaultReadTimeout(100 * time.Millisecond)
	s.HandleFunc("POST /x", func(w http.ResponseWriter, r *http.Request) {
		var v any
		if err := JSON().Decode(r.Body, &v); err != nil {
			WriteValidationError(w, r, err)
		}
	})
	addr := freeAddr(t)
	go func() { _ = s.Start(addr) }()
	t.Cleanup(func() { _ = s.Stop(t.Context()) })
	var c net.Conn
	for range 50 {
		var err error
		if c, err = net.Dial("tcp", addr); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if c == nil {
		t.Fatal("the server never listened")
	}
	defer c.Close()
	fmt.Fprintf(c, "POST /x HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{\"a\":")
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	raw, _ := io.ReadAll(c)
	if !strings.HasPrefix(string(raw), "HTTP/1.1 408") || !strings.Contains(string(raw), `{"message":"request timeout"}`) || strings.Contains(string(raw), "127.0.0.1") {
		t.Errorf("got:\n%s", raw)
	}
}
