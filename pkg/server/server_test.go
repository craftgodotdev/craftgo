package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/craftgodotdev/craftgo/pkg/log"
)

// newTestServer returns a Server with the defaults.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	return New(nil)
}

// finalize returns the handler Start serves.
func finalize(s *Server) http.Handler {
	return s.Handler()
}

func TestServerHandleFuncAndDefaults(t *testing.T) {
	s := newTestServer(t)
	s.HandleFunc("GET /ping", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("pong"))
	})
	rec := httptest.NewRecorder()
	finalize(s).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ping", nil))
	if rec.Body.String() != "pong" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestServerRecoveryConvertsPanic(t *testing.T) {
	s := newTestServer(t)
	s.HandleFunc("GET /boom", func(_ http.ResponseWriter, _ *http.Request) {
		panic("kaboom")
	})
	rec := httptest.NewRecorder()
	finalize(s).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

// The Recovery a Server installs logs to log.Default as it is when the panic happens.
func TestServerRecoveryLogsToTheCurrentDefault(t *testing.T) {
	s := newTestServer(t)
	s.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) { panic("boom") })
	h := finalize(s)
	logs := observeLogs(t)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/boom", nil))
	if n := logs.FilterMessage("panic recovered").Len(); n != 1 {
		t.Errorf("want the panic on the current default logger, got %d lines", n)
	}
	if s.Logger() != log.Default() {
		t.Error("Logger must return log.Default")
	}
}

// A panic after the response is committed is logged and aborts the connection, so the client
// never reads a clean end: a buffered body is dropped and a flushed stream is cut off.
func TestServerRecoveryAfterCommitAbortsTheConnection(t *testing.T) {
	logs := observeLogs(t)
	s := newTestServer(t)
	s.HandleFunc("GET /written", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"partial":true`))
		panic("after write")
	})
	s.HandleFunc("GET /flushed", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: 1\n\n"))
		w.(http.Flusher).Flush()
		panic("after flush")
	})
	srv := httptest.NewServer(finalize(s))
	defer srv.Close()
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

	if resp, err := client.Get(srv.URL + "/written"); err == nil {
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err == nil {
			t.Errorf("GET /written read %d %q to a clean end, want the connection aborted", resp.StatusCode, body)
		}
	}
	resp, err := client.Get(srv.URL + "/flushed")
	if err != nil {
		t.Fatalf("GET /flushed: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "data: 1\n\n" || err == nil {
		t.Errorf("GET /flushed: %d %q, read error %v; want 200 and the flushed event, then the cut", resp.StatusCode, body, err)
	}
	if n := logs.FilterMessageSnippet("after response committed").Len(); n != 2 {
		t.Errorf("want each panic logged as after-commit once, got %d lines", n)
	}
}

// A handler that panics with http.ErrAbortHandler has its connection aborted, before or
// after the response is committed, and is not logged as a crash.
func TestServerRecoveryLetsAnAbortedHandlerAbortTheConnection(t *testing.T) {
	logs := observeLogs(t)
	s := newTestServer(t)
	s.HandleFunc("GET /abort", func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })
	s.HandleFunc("GET /abort-mid-stream", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	})
	srv := httptest.NewServer(finalize(s))
	defer srv.Close()

	if resp, err := srv.Client().Get(srv.URL + "/abort"); err == nil {
		_ = resp.Body.Close()
		t.Errorf("GET /abort answered %d, want the connection aborted", resp.StatusCode)
	}
	resp, err := srv.Client().Get(srv.URL + "/abort-mid-stream")
	if err != nil {
		t.Fatalf("GET /abort-mid-stream: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if body, err := io.ReadAll(resp.Body); err == nil {
		t.Errorf("GET /abort-mid-stream read %q to its end, want the body cut off", body)
	}
	if n := logs.FilterMessageSnippet("panic recovered").Len(); n != 0 {
		t.Errorf("an aborted handler was logged as a panic %d time(s)", n)
	}
}

// A status net/http rejects commits nothing, so Recovery answers 500, through AccessLog and
// Compress too.
func TestAnInvalidStatusIsAnswered500(t *testing.T) {
	observeLogs(t)
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	for _, code := range []int{0, 99, 1000} {
		for name, chain := range map[string][]Middleware{
			"default chain":           nil,
			"access log and compress": {AccessLog(log.Default()), Compress()},
		} {
			t.Run(fmt.Sprintf("%s/%d", name, code), func(t *testing.T) {
				s := newTestServer(t)
				for _, mw := range chain {
					s.Use(mw)
				}
				s.HandleFunc("GET /x", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) })
				srv := httptest.NewServer(finalize(s))
				defer srv.Close()
				req, err := http.NewRequest(http.MethodGet, srv.URL+"/x", nil)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Accept-Encoding", "gzip")
				resp, err := client.Do(req)
				if err != nil {
					t.Fatalf("WriteHeader(%d): %v, want a 500", code, err)
				}
				_ = resp.Body.Close()
				if resp.StatusCode != http.StatusInternalServerError {
					t.Errorf("WriteHeader(%d): status %d, want 500", code, resp.StatusCode)
				}
			})
		}
	}
}

// WriteValidationError leaves a committed response untouched.
func TestWriteValidationErrorSkipsPostCommit(t *testing.T) {
	s := newTestServer(t)
	s.HandleFunc("GET /v", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
		WriteValidationError(w, r, errBadField)
	})
	rec := httptest.NewRecorder()
	finalize(s).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("post-commit validation must not rewrite status, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Errorf("expected partial body intact, got %q", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "bad field") {
		t.Errorf("validation error must not leak into committed body: %q", rec.Body.String())
	}
}

// errBadField is a stand-in validation error.
var errBadField = stringError("bad field")

type stringError string

func (e stringError) Error() string { return string(e) }

// A panic under a WithLimits timeout reaches Recovery.
func TestWithLimitsTimeoutPanicReachesRecovery(t *testing.T) {
	core := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("inside timeout")
	})
	guarded := WithLimits(core, Limits{Timeout: 100 * time.Millisecond})
	chain := Recovery(log.Discard())(guarded)
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 from Recovery, got %d (body=%q)", rec.Code, rec.Body.String())
	}
}

// A WithLimits timeout cancels the request context.
func TestWithLimitsTimeoutContextCancellation(t *testing.T) {
	core := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		_, _ = w.Write([]byte("cancelled:" + r.Context().Err().Error()))
	})
	guarded := WithLimits(core, Limits{Timeout: 50 * time.Millisecond})
	rec := httptest.NewRecorder()
	guarded.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if !strings.Contains(rec.Body.String(), "cancelled:") {
		t.Errorf("handler did not observe context cancel: %q", rec.Body.String())
	}
}

// WithLimits answers 413 to an oversized Content-Length the handler never reads.
func TestWithLimitsContentLengthPreCheck(t *testing.T) {
	core := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	guarded := WithLimits(core, Limits{MaxBodySize: 10})
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(""))
	req.ContentLength = 1024
	rec := httptest.NewRecorder()
	guarded.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413 from Content-Length pre-check, got %d", rec.Code)
	}
}

func TestServerHealthEndpoints(t *testing.T) {
	s := newTestServer(t)
	called := int32(0)
	s.RegisterHealthCheck("db", time.Second, func(_ context.Context) error {
		atomic.AddInt32(&called, 1)
		return nil
	})
	h := finalize(s)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("liveness: got %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK || atomic.LoadInt32(&called) != 1 {
		t.Errorf("readyz: code=%d called=%d body=%s", rec.Code, called, rec.Body.String())
	}
}

func TestServerHealthCheckFailure(t *testing.T) {
	s := newTestServer(t)
	s.RegisterHealthCheck("bad", 50*time.Millisecond, func(_ context.Context) error {
		return errors.New("down")
	})
	rec := httptest.NewRecorder()
	finalize(s).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// A check that returns an error fails readiness whatever the error's text.
func TestServerHealthCheckErrorTextIsNeverHealthy(t *testing.T) {
	s := newTestServer(t)
	s.RegisterHealthCheck("cache", time.Second, func(context.Context) error { return errors.New("ok") })
	rec := httptest.NewRecorder()
	finalize(s).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"not_ready"`) {
		t.Errorf("status %d, body %s; want 503 not_ready", rec.Code, rec.Body.String())
	}
}

// A check that panics fails readiness, and the panic is logged under the check's name.
func TestServerHealthCheckPanicFailsTheProbe(t *testing.T) {
	logs := observeLogs(t)
	s := newTestServer(t)
	s.RegisterHealthCheck("db", time.Second, func(context.Context) error { return nil })
	s.RegisterHealthCheck("cache", time.Second, func(context.Context) error { panic("cache client is nil") })
	rec := httptest.NewRecorder()
	finalize(s).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	if rec.Code != http.StatusServiceUnavailable || body.Status != "not_ready" {
		t.Errorf("status %d, body %s; want 503 not_ready", rec.Code, rec.Body.String())
	}
	if got, want := body.Checks["cache"], "panic: cache client is nil"; got != want {
		t.Errorf("cache check = %q, want %q", got, want)
	}
	if got := body.Checks["db"]; got != "ok" {
		t.Errorf("db check = %q, want ok", got)
	}
	entries := logs.FilterMessage("panic recovered in readiness check").AllUntimed()
	if len(entries) != 1 {
		t.Fatalf("want the panic logged once, got %d lines", len(entries))
	}
	if fields := entries[0].ContextMap(); fields["check"] != "cache" || fields["stack"] == "" {
		t.Errorf("log fields %v, want the check's name and the stack", fields)
	}
}

func TestServerWithoutDefaultHealth(t *testing.T) {
	s := New(nil, WithoutDefaultHealth())
	rec := httptest.NewRecorder()
	finalize(s).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 when health disabled, got %d", rec.Code)
	}
}

// A custom not-found handler answers what the mux answers 404; a method mismatch keeps its
// 405 with Allow, and an unclean path its redirect.
func TestSetHandleNotFoundTakesOnlyThe404s(t *testing.T) {
	s := newTestServer(t)
	s.HandleFunc("GET /only-get", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	s.SetHandleNotFound(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"no such route"}`))
	}))
	h := finalize(s)
	for _, tc := range []struct {
		method, path string
		status       int
		header, want string
	}{
		{http.MethodGet, "/only-get", http.StatusOK, "", ""},
		{http.MethodPost, "/only-get", http.StatusMethodNotAllowed, "Allow", "GET"},
		{http.MethodGet, "/missing", http.StatusNotFound, "", ""},
		{http.MethodGet, "/a/../missing", http.StatusTemporaryRedirect, "Location", "/missing"},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != tc.status {
			t.Errorf("%s %s: status %d, want %d", tc.method, tc.path, rec.Code, tc.status)
		}
		if tc.header != "" && !strings.Contains(rec.Header().Get(tc.header), tc.want) {
			t.Errorf("%s %s: %s = %q, want it to hold %q", tc.method, tc.path, tc.header, rec.Header().Get(tc.header), tc.want)
		}
		if custom := strings.Contains(rec.Body.String(), "no such route"); custom != (tc.status == http.StatusNotFound) {
			t.Errorf("%s %s: custom handler answered = %v", tc.method, tc.path, custom)
		}
	}
}

// SetHandleNotFound(nil) restores the default 404.
func TestSetHandleNotFoundNilRestoresTheDefault(t *testing.T) {
	s := newTestServer(t)
	s.SetHandleNotFound(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	s.SetHandleNotFound(nil)
	rec := httptest.NewRecorder()
	finalize(s).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if rec.Code != http.StatusNotFound || rec.Body.String() != `{"message":"not found"}`+"\n" {
		t.Errorf("status %d, body %q; want the default JSON 404", rec.Code, rec.Body.String())
	}
}

func TestServerWithCustomHealthPaths(t *testing.T) {
	s := New(nil, WithHealthPaths(HealthPaths{Liveness: "/live", Readiness: "/ready"}))
	rec := httptest.NewRecorder()
	finalize(s).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/live", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 on /live, got %d", rec.Code)
	}
}

func TestAccessLogMiddleware(t *testing.T) {
	s := newTestServer(t).Use(AccessLog(log.Discard()))
	s.HandleFunc("GET /a", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	rec := httptest.NewRecorder()
	finalize(s).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/a", nil))
	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d", rec.Code)
	}
}

// AccessLog logs every request's method, path and status except the skipped paths.
func TestAccessLogSkipPaths(t *testing.T) {
	logs := observeLogs(t)
	s := newTestServer(t).Use(AccessLog(log.Default(), AccessLogSkipPaths("/metrics")))
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	s.HandleFunc("GET /metrics", ok)
	s.HandleFunc("GET /a", ok)
	h := finalize(s)
	for _, path := range []string{"/metrics", "/a", "/missing"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	var got []string
	for _, e := range logs.FilterMessage("http access").All() {
		fields := e.ContextMap()
		got = append(got, fmt.Sprintf("%s %v %v", fields["method"], fields["path"], fields["status"]))
	}
	want := []string{"GET /a 200", "GET /missing 404"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("access log lines = %v, want %v", got, want)
	}
}

// AccessLog records the status the client receives: the first final status written, 200 when
// a Flush or nothing sends the head.
func TestAccessLogRecordsTheFirstFinalStatus(t *testing.T) {
	for name, tc := range map[string]struct {
		handler http.HandlerFunc
		want    int64
	}{
		"superfluous status": {func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusCreated)
			w.WriteHeader(http.StatusInternalServerError)
		}, http.StatusCreated},
		"early hints first": {func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusEarlyHints)
			w.WriteHeader(http.StatusNoContent)
		}, http.StatusNoContent},
		"flush only":      {func(w http.ResponseWriter, _ *http.Request) { w.(http.Flusher).Flush() }, http.StatusOK},
		"nothing written": {func(http.ResponseWriter, *http.Request) {}, http.StatusOK},
	} {
		t.Run(name, func(t *testing.T) {
			logs := observeLogs(t)
			AccessLog(log.Default())(tc.handler).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
			entries := logs.FilterMessage("http access").All()
			if len(entries) != 1 {
				t.Fatalf("want 1 access line, got %d", len(entries))
			}
			if got := entries[0].ContextMap()["status"]; got != tc.want {
				t.Errorf("status = %v, want %d", got, tc.want)
			}
		})
	}
}

// A client that left before anything was written is logged with status 499; one that left
// after the head was written keeps that status.
func TestAccessLogRecords499ForAGoneClient(t *testing.T) {
	for name, tc := range map[string]struct {
		handler http.HandlerFunc
		want    int64
	}{
		"nothing written": {func(w http.ResponseWriter, r *http.Request) { WriteError(w, r, r.Context().Err()) }, 499},
		"head written":    {func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) }, http.StatusCreated},
	} {
		t.Run(name, func(t *testing.T) {
			logs := observeLogs(t)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			req := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(ctx)
			AccessLog(log.Default())(tc.handler).ServeHTTP(httptest.NewRecorder(), req)
			entries := logs.FilterMessage("http access").All()
			if len(entries) != 1 || entries[0].ContextMap()["status"] != tc.want {
				t.Errorf("access lines %v, want one with status %d", entries, tc.want)
			}
		})
	}
}

// A client that disconnects while its handler waits is logged with status 499.
func TestAccessLogRecords499OverAConnection(t *testing.T) {
	logs := observeLogs(t)
	s := newTestServer(t).Use(AccessLog(log.Default()))
	s.HandleFunc("GET /wait", func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		WriteError(w, r, r.Context().Err())
	})
	ts := httptest.NewServer(finalize(s))
	defer ts.Close()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/wait", nil)
	if err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(20*time.Millisecond, cancel)
	if _, err := ts.Client().Do(req); err == nil {
		t.Fatal("the request outlived its canceled context")
	}
	for wait := time.Now().Add(5 * time.Second); logs.FilterMessage("http access").Len() == 0 && time.Now().Before(wait); {
		time.Sleep(5 * time.Millisecond)
	}
	entries := logs.FilterMessage("http access").All()
	if len(entries) != 1 || entries[0].ContextMap()["status"] != int64(499) {
		t.Errorf("access lines %v, want one with status 499", entries)
	}
}

// No Use middleware sees the health probes, on default or custom paths.
func TestProbesBypassMiddlewareChain(t *testing.T) {
	for name, opts := range map[string][]Option{
		"default paths": nil,
		"custom paths":  {WithHealthPaths(HealthPaths{Liveness: "/live", Readiness: "/ready"})},
	} {
		t.Run(name, func(t *testing.T) {
			s := New(nil, opts...)
			var seen []string
			s.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					seen = append(seen, r.URL.Path)
					next.ServeHTTP(w, r)
				})
			})
			s.HandleFunc("GET /a", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
			h := finalize(s)
			for _, path := range []string{s.healthPaths.Liveness, s.healthPaths.Readiness, "/a"} {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
				if rec.Code != http.StatusOK {
					t.Errorf("GET %s: status %d", path, rec.Code)
				}
			}
			if strings.Join(seen, ",") != "/a" {
				t.Errorf("middleware saw %v, want only /a", seen)
			}
		})
	}
}

func TestBodyLimitMiddleware(t *testing.T) {
	s := newTestServer(t).Use(BodyLimit(4))
	s.HandleFunc("POST /b", func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	finalize(s).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/b", strings.NewReader("toolong")))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413, got %d", rec.Code)
	}
}

func TestTimeoutMiddleware(t *testing.T) {
	s := newTestServer(t).Use(Timeout(10 * time.Millisecond))
	s.HandleFunc("GET /slow", func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	finalize(s).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/slow", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 from timeout, got %d", rec.Code)
	}
}

func TestServerSetters(t *testing.T) {
	s := newTestServer(t)
	s.SetDefaultReadTimeout(time.Second).
		SetDefaultWriteTimeout(2*time.Second).
		SetDefaultMaxBodySize(1024).
		SetDefaultMaxHeaderSize(8).
		SetLogger(s.Logger()).
		RegisterMiddleware("auth", func(h http.Handler) http.Handler { return h })
	if err := s.SetJSONCodec(defaultCodec{}); err != nil {
		t.Fatal(err)
	}
	if s.Codec() == nil || s.Logger() == nil {
		t.Error("codec/logger should be non-nil")
	}
	if s.Mux() == nil {
		t.Error("mux should be non-nil")
	}
}

// Setters may run on another goroutine than route registration and Handler.
func TestServerConfigurationAcrossGoroutines(t *testing.T) {
	s := newTestServer(t)
	var wg sync.WaitGroup
	wg.Go(func() {
		s.SetDefaultReadTimeout(time.Second).
			SetDefaultWriteTimeout(time.Second).
			SetDefaultHandlerTimeout(time.Second).
			SetDefaultMaxBodySize(1 << 20).
			SetDefaultMaxHeaderSize(16).
			SetCORS(CORSPermissive()).
			SetLogger(log.Default())
	})
	wg.Go(func() {
		s.HandleFunc("GET /a", func(http.ResponseWriter, *http.Request) {})
		s.Handle("GET /b", http.NotFoundHandler())
		_ = s.Handler()
	})
	wg.Wait()
}

func TestCORSMiddleware(t *testing.T) {
	s := newTestServer(t).SetCORS(CORSOptions{
		AllowedOrigins:   []string{"https://app.example.com", "https://*.partner.com"},
		AllowedMethods:   []string{"GET", "POST"},
		AllowedHeaders:   []string{"Content-Type"},
		ExposedHeaders:   []string{"X-Trace-Id"},
		AllowCredentials: true,
		MaxAge:           time.Hour,
	})
	s.HandleFunc("GET /c", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := finalize(s)

	// Allowed origin → header echoed.
	req := httptest.NewRequest(http.MethodGet, "/c", nil)
	req.Header.Set("Origin", "https://app.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Errorf("origin header = %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}

	// Wildcard match.
	req = httptest.NewRequest(http.MethodGet, "/c", nil)
	req.Header.Set("Origin", "https://x.partner.com")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://x.partner.com" {
		t.Errorf("wildcard origin header = %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}

	// Preflight: a genuine CORS preflight carries Access-Control-Request-Method.
	req = httptest.NewRequest(http.MethodOptions, "/c", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Error("missing allow-methods on preflight")
	}

	// Disallowed origin → no header.
	req = httptest.NewRequest(http.MethodGet, "/c", nil)
	req.Header.Set("Origin", "https://evil.com")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("expected empty origin header, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORSPresets(t *testing.T) {
	if matchOrigin("https://x", CORSPermissive().AllowedOrigins) != "*" {
		t.Error("permissive preset should accept any origin")
	}
	strict := CORSStrict("https://app.com")
	if matchOrigin("https://app.com", strict.AllowedOrigins) != "https://app.com" {
		t.Error("strict preset should accept its origin")
	}
	if matchOrigin("https://other.com", strict.AllowedOrigins) != "" {
		t.Error("strict preset should reject other origins")
	}
	if matchOrigin("", []string{"*"}) != "" {
		t.Error("empty origin should yield empty match")
	}
	if !matchWildcard("a*c", "abc") || matchWildcard("a*c", "ab") {
		t.Error("wildcard match logic broken")
	}
	if matchWildcard("abc", "abc") != true {
		t.Error("wildcard match should fall back to equality when no '*' present")
	}
}

func TestCodecRoundTrip(t *testing.T) {
	c := defaultCodec{}
	var buf strings.Builder
	if err := c.Encode(&buf, map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	var out map[string]int
	if err := c.Decode(strings.NewReader(buf.String()), &out); err != nil {
		t.Fatal(err)
	}
	if out["a"] != 1 {
		t.Errorf("round trip lost value: %v", out)
	}
}

// markerCodec prefixes every encoding with /*MARK*/.
type markerCodec struct{ defaultCodec }

func (markerCodec) Encode(w io.Writer, v any) error {
	if _, err := w.Write([]byte("/*MARK*/")); err != nil {
		return err
	}
	return defaultCodec{}.Encode(w, v)
}

func TestGlobalJSONCodecSwapTakesEffect(t *testing.T) {
	t.Cleanup(func() { _ = SetGlobalJSONCodec(defaultCodec{}) })
	if err := SetGlobalJSONCodec(markerCodec{}); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	if err := JSON().Encode(&buf, map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(buf.String(), "/*MARK*/") {
		t.Errorf("global codec swap not picked up by JSON(): %q", buf.String())
	}
}

func TestServerSetJSONCodecPropagatesToGlobal(t *testing.T) {
	t.Cleanup(func() { SetGlobalJSONCodec(defaultCodec{}) })
	New(nil).SetJSONCodec(markerCodec{})
	var buf strings.Builder
	_ = JSON().Encode(&buf, map[string]int{"b": 2})
	if !strings.HasPrefix(buf.String(), "/*MARK*/") {
		t.Errorf("Server.SetJSONCodec must update the global codec; got %q", buf.String())
	}
}

// Codec reports the codec JSON returns, after a process-wide swap and under strict JSON.
func TestServerCodecIsTheCodecInUse(t *testing.T) {
	t.Cleanup(func() {
		_ = SetStrictJSON(false)
		_ = SetGlobalJSONCodec(nil)
	})
	s := New(nil)
	if err := SetGlobalJSONCodec(markerCodec{}); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%T", s.Codec()); got != "server.markerCodec" {
		t.Errorf("after SetGlobalJSONCodec: Codec() is %s", got)
	}
	if err := s.SetStrictJSON(true); err != nil {
		t.Fatal(err)
	}
	if got, want := fmt.Sprintf("%T", s.Codec()), fmt.Sprintf("%T", JSON()); got != want {
		t.Errorf("under strict JSON: Codec() is %s, JSON() is %s", got, want)
	}
}

func TestServerStopBeforeStart(t *testing.T) {
	if err := New(nil).Stop(context.Background()); err != nil {
		t.Errorf("Stop before Start should be no-op, got %v", err)
	}
}

func TestServerStartAndStop(t *testing.T) {
	s := New(nil)
	s.HandleFunc("GET /smoke", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	go func() { _ = s.Start("127.0.0.1:0") }()
	time.Sleep(20 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = s.Stop(ctx)
}

// AccessLogFields adds its fields after the handler ran, so the matched route is available.
func TestAccessLogFields(t *testing.T) {
	logs := observeLogs(t)
	s := newTestServer(t).Use(AccessLog(log.Default(), AccessLogFields(func(r *http.Request) []log.Field {
		return []log.Field{log.String("route", r.Pattern), log.String("ua", r.UserAgent())}
	})))
	s.HandleFunc("GET /items/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/items/7", nil)
	req.Header.Set("User-Agent", "probe/1")
	finalize(s).ServeHTTP(httptest.NewRecorder(), req)
	entries := logs.FilterMessage("http access").All()
	if len(entries) != 1 {
		t.Fatalf("want 1 access line, got %d", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["route"] != "GET /items/{id}" || fields["ua"] != "probe/1" || fields["status"] != int64(http.StatusOK) {
		t.Errorf("fields = %v", fields)
	}
}
