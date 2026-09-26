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

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/craftgodotdev/craftgo/pkg/log"
)

// HandleFunc registers a handler that Handler serves.
func TestServerHandleFunc(t *testing.T) {
	s := New(nil)
	s.HandleFunc("GET /ping", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("pong"))
	})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ping", nil))
	if rec.Body.String() != "pong" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

// The Recovery a Server installs logs to log.Default as it is when the panic happens.
func TestServerRecoveryLogsToTheCurrentDefault(t *testing.T) {
	s := New(nil)
	s.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) { panic("boom") })
	h := s.Handler()
	logs := observeLogs(t)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/boom", nil))
	if n := logs.FilterMessage("panic recovered").Len(); n != 1 {
		t.Errorf("want the panic on the current default logger, got %d lines", n)
	}
}

// An access log built from Logger writes to the logger a later SetLogger installs.
func TestAccessLogFollowsSetLogger(t *testing.T) {
	observeLogs(t)
	s := New(nil)
	s.Use(AccessLog(s.Logger()))
	s.HandleFunc("GET /a", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := s.Handler()
	core, logs := observer.New(zapcore.InfoLevel)
	s.SetLogger(log.NewZap(zap.New(core)))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/a", nil))
	if n := logs.FilterMessage("http access").Len(); n != 1 {
		t.Errorf("access lines on the logger SetLogger installed = %d, want 1", n)
	}
}

// The logger Logger returns compares equal to another, and its access lines name AccessLog's
// own code as their caller.
func TestLoggerComparesAndKeepsTheCaller(t *testing.T) {
	observeLogs(t)
	s := New(nil)
	func() {
		defer func() {
			if p := recover(); p != nil {
				t.Errorf("comparing Logger() results panics: %v", p)
			}
		}()
		if s.Logger() != s.Logger() {
			t.Error("Logger() != Logger()")
		}
	}()
	core, logs := observer.New(zapcore.InfoLevel)
	s.SetLogger(log.NewZap(zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))))
	s.Use(AccessLog(s.Logger()))
	s.HandleFunc("GET /a", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	s.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/a", nil))
	lines := logs.FilterMessage("http access").All()
	if len(lines) != 1 || !strings.HasSuffix(lines[0].Caller.File, "server/middleware.go") {
		t.Errorf("access lines %v, want one whose caller is server/middleware.go", lines)
	}
}

// A panic after the response is committed is logged and aborts the connection, so the client
// never reads a clean end: a buffered body is dropped and a flushed stream is cut off.
func TestServerRecoveryAfterCommitAbortsTheConnection(t *testing.T) {
	logs := observeLogs(t)
	s := New(nil)
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
	srv := httptest.NewServer(s.Handler())
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
	s := New(nil)
	s.HandleFunc("GET /abort", func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })
	s.HandleFunc("GET /abort-mid-stream", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	})
	srv := httptest.NewServer(s.Handler())
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
				s := New(nil)
				for _, mw := range chain {
					s.Use(mw)
				}
				s.HandleFunc("GET /x", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) })
				srv := httptest.NewServer(s.Handler())
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
	s := New(nil)
	s.HandleFunc("GET /v", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
		WriteValidationError(w, r, errBadField)
	})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v", nil))
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
	s := New(nil)
	called := int32(0)
	s.RegisterHealthCheck("db", time.Second, func(_ context.Context) error {
		atomic.AddInt32(&called, 1)
		return nil
	})
	h := s.Handler()
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

// A check that returns an error fails readiness whatever the error's text.
func TestServerHealthCheckErrorTextIsNeverHealthy(t *testing.T) {
	s := New(nil)
	s.RegisterHealthCheck("cache", time.Second, func(context.Context) error { return errors.New("ok") })
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"not_ready"`) {
		t.Errorf("status %d, body %s; want 503 not_ready", rec.Code, rec.Body.String())
	}
}

// A check that panics fails readiness, and the panic is logged under the check's name.
func TestServerHealthCheckPanicFailsTheProbe(t *testing.T) {
	logs := observeLogs(t)
	s := New(nil)
	s.RegisterHealthCheck("db", time.Second, func(context.Context) error { return nil })
	s.RegisterHealthCheck("cache", time.Second, func(context.Context) error { panic("cache client is nil") })
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

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
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 when health disabled, got %d", rec.Code)
	}
}

// A custom not-found handler answers what the mux answers 404; a method mismatch keeps its
// 405 with Allow, and an unclean path its redirect.
func TestSetHandleNotFoundTakesOnlyThe404s(t *testing.T) {
	s := New(nil)
	s.HandleFunc("GET /only-get", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	s.SetHandleNotFound(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"no such route"}`))
	}))
	h := s.Handler()
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
	s := New(nil)
	s.SetHandleNotFound(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	s.SetHandleNotFound(nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if rec.Code != http.StatusNotFound || rec.Body.String() != `{"message":"not found"}`+"\n" {
		t.Errorf("status %d, body %q; want the default JSON 404", rec.Code, rec.Body.String())
	}
}

func TestServerWithCustomHealthPaths(t *testing.T) {
	s := New(nil, WithHealthPaths(HealthPaths{Liveness: "/live", Readiness: "/ready"}))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/live", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 on /live, got %d", rec.Code)
	}
}

func TestAccessLogMiddleware(t *testing.T) {
	s := New(nil).Use(AccessLog(log.Discard()))
	s.HandleFunc("GET /a", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/a", nil))
	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d", rec.Code)
	}
}

// AccessLog logs every request's method, path and status except the skipped paths.
func TestAccessLogSkipPaths(t *testing.T) {
	logs := observeLogs(t)
	s := New(nil).Use(AccessLog(log.Default(), AccessLogSkipPaths("/metrics")))
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	s.HandleFunc("GET /metrics", ok)
	s.HandleFunc("GET /a", ok)
	h := s.Handler()
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
	s := New(nil).Use(AccessLog(log.Default()))
	s.HandleFunc("GET /wait", func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		WriteError(w, r, r.Context().Err())
	})
	ts := httptest.NewServer(s.Handler())
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

// WithTelemetry wraps Recovery and every Use middleware, so it sees the 500 a panic becomes,
// and the health probes bypass it; nil installs nothing.
func TestWithTelemetryWrapsRecoveryButNotTheProbes(t *testing.T) {
	observeLogs(t)
	var seen []string
	telemetry := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = append(seen, "telemetry")
			tw := &trackingWriter{ResponseWriter: w}
			next.ServeHTTP(tw, r)
			seen = append(seen, fmt.Sprintf("%s %d", r.URL.Path, tw.Status()))
		})
	}
	s := New(nil, WithTelemetry(telemetry))
	s.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = append(seen, "use")
			next.ServeHTTP(w, r)
		})
	})
	s.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) { panic("boom") })
	h := s.Handler()
	for _, path := range []string{"/boom", DefaultLivenessPath, DefaultReadinessPath} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	if got, want := strings.Join(seen, ","), "telemetry,use,/boom 500"; got != want {
		t.Errorf("saw %s, want %s", got, want)
	}

	plain := New(nil, WithTelemetry(nil))
	plain.HandleFunc("GET /ok", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	rec := httptest.NewRecorder()
	plain.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ok", nil))
	if rec.Code != http.StatusNoContent {
		t.Errorf("WithTelemetry(nil): status %d, want the route's 204", rec.Code)
	}
}

// WithTelemetry(nil) installs nothing and keeps what an earlier WithTelemetry installed, as
// rpc.WithStatsHandler(nil) does.
func TestWithTelemetryNilKeepsTheEarlierOne(t *testing.T) {
	seen := 0
	telemetry := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen++
			next.ServeHTTP(w, r)
		})
	}
	s := New(nil, WithTelemetry(telemetry), WithTelemetry(nil))
	s.HandleFunc("GET /ok", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	s.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ok", nil))
	if seen != 1 {
		t.Errorf("the telemetry middleware ran %d times, want 1", seen)
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
			h := s.Handler()
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

func TestTimeoutMiddleware(t *testing.T) {
	s := New(nil).Use(Timeout(10 * time.Millisecond))
	s.HandleFunc("GET /slow", func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/slow", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 from timeout, got %d", rec.Code)
	}
}

// Setters may run on another goroutine than route registration and Handler.
func TestServerConfigurationAcrossGoroutines(t *testing.T) {
	s := New(nil)
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

// Each CORSOptions field reaches the response: the origin, credentials and exposed headers on
// every answer to an allowed origin, the methods, headers, max age and private-network grant on
// a preflight too; a disallowed origin gets none of them.
func TestCORSMiddleware(t *testing.T) {
	s := New(nil).SetCORS(CORSOptions{
		AllowedOrigins:      []string{"https://app.example.com", "https://*.partner.com"},
		AllowedMethods:      []string{"GET", "POST"},
		AllowedHeaders:      []string{"Content-Type", "X-Request"},
		ExposedHeaders:      []string{"X-Trace-Id", "X-Rate"},
		AllowCredentials:    true,
		MaxAge:              time.Hour,
		AllowPrivateNetwork: true,
	})
	s.HandleFunc("GET /c", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := s.Handler()
	check := func(name, method, origin string, wantStatus int, want map[string]string) {
		t.Helper()
		req := httptest.NewRequest(method, "/c", nil)
		req.Header.Set("Origin", origin)
		if method == http.MethodOptions {
			req.Header.Set("Access-Control-Request-Method", "POST")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != wantStatus {
			t.Errorf("%s: status %d, want %d", name, rec.Code, wantStatus)
		}
		for key, value := range want {
			if got := rec.Header().Get(key); got != value {
				t.Errorf("%s: %s = %q, want %q", name, key, got, value)
			}
		}
	}

	check("an allowed origin", http.MethodGet, "https://app.example.com", http.StatusOK, map[string]string{
		"Access-Control-Allow-Origin":      "https://app.example.com",
		"Vary":                             "Origin",
		"Access-Control-Allow-Credentials": "true",
		"Access-Control-Expose-Headers":    "X-Trace-Id, X-Rate",
		"Access-Control-Allow-Methods":     "",
	})
	check("a wildcard origin", http.MethodGet, "https://x.partner.com", http.StatusOK, map[string]string{
		"Access-Control-Allow-Origin": "https://x.partner.com",
	})
	check("a preflight", http.MethodOptions, "https://app.example.com", http.StatusNoContent, map[string]string{
		"Access-Control-Allow-Origin":          "https://app.example.com",
		"Access-Control-Allow-Credentials":     "true",
		"Access-Control-Allow-Methods":         "GET, POST",
		"Access-Control-Allow-Headers":         "Content-Type, X-Request",
		"Access-Control-Max-Age":               "3600",
		"Access-Control-Allow-Private-Network": "true",
	})
	check("a disallowed origin", http.MethodGet, "https://evil.com", http.StatusOK, map[string]string{
		"Access-Control-Allow-Origin":      "",
		"Access-Control-Allow-Credentials": "",
		"Access-Control-Expose-Headers":    "",
	})
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
	resetCodec(t)
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
	resetCodec(t)
	New(nil).SetJSONCodec(markerCodec{})
	var buf strings.Builder
	_ = JSON().Encode(&buf, map[string]int{"b": 2})
	if !strings.HasPrefix(buf.String(), "/*MARK*/") {
		t.Errorf("Server.SetJSONCodec must update the global codec; got %q", buf.String())
	}
}

// Codec reports the codec JSON returns, after a process-wide swap and under strict JSON.
func TestServerCodecIsTheCodecInUse(t *testing.T) {
	resetCodec(t)
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

// Start serves Handler until Stop and then returns nil, on an http.Server with the read and write
// timeouts and the header cap the setters chose: 30s, none and 32 KB by default.
func TestServerStartServesUntilStop(t *testing.T) {
	for _, c := range []struct {
		name        string
		configure   func(*Server)
		read, write time.Duration
		headerBytes int
	}{
		{"defaults", func(*Server) {}, 30 * time.Second, 0, 32 << 10},
		{"setters", func(s *Server) {
			s.SetDefaultReadTimeout(time.Second).SetDefaultWriteTimeout(2 * time.Second).SetDefaultMaxHeaderSize(8)
		}, time.Second, 2 * time.Second, 8 << 10},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := New(nil)
			c.configure(s)
			s.HandleFunc("GET /smoke", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
			addr := freeAddr(t)
			done := make(chan error, 1)
			go func() { done <- s.Start(addr) }()
			waitForStatus(t, "http://"+addr+"/smoke", http.StatusTeapot)

			s.mu.Lock()
			srv := s.httpSrv
			s.mu.Unlock()
			if srv.ReadTimeout != c.read || srv.WriteTimeout != c.write || srv.MaxHeaderBytes != c.headerBytes {
				t.Errorf("http.Server read timeout %v, write timeout %v, header cap %d bytes; want %v, %v, %d",
					srv.ReadTimeout, srv.WriteTimeout, srv.MaxHeaderBytes, c.read, c.write, c.headerBytes)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.Stop(ctx); err != nil {
				t.Fatalf("Stop: %v", err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("Start returned %v after Stop, want nil", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Start did not return after Stop")
			}
		})
	}
}

// AccessLogFields adds its fields after the handler ran, so the matched route is available.
func TestAccessLogFields(t *testing.T) {
	logs := observeLogs(t)
	s := New(nil).Use(AccessLog(log.Default(), AccessLogFields(func(r *http.Request) []log.Field {
		return []log.Field{log.String("route", r.Pattern), log.String("ua", r.UserAgent())}
	})))
	s.HandleFunc("GET /items/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/items/7", nil)
	req.Header.Set("User-Agent", "probe/1")
	s.Handler().ServeHTTP(httptest.NewRecorder(), req)
	entries := logs.FilterMessage("http access").All()
	if len(entries) != 1 {
		t.Fatalf("want 1 access line, got %d", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["route"] != "GET /items/{id}" || fields["ua"] != "probe/1" || fields["status"] != int64(http.StatusOK) {
		t.Errorf("fields = %v", fields)
	}
}
