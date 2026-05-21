package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestServer builds a Server, runs handler at "GET /ping", and returns
// the wired http.Handler. We bypass Start so each test owns its own
// httptest.NewServer.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	return New(nil)
}

// finalize emulates Start's middleware chain build without binding a
// listener. Tests use it to exercise the chain in-process.
func finalize(s *Server) http.Handler {
	if !s.noHealth {
		s.mux.Handle(s.healthPaths.Liveness, s.livenessHandler())
		s.mux.Handle(s.healthPaths.Readiness, s.readinessHandler())
	}
	var h http.Handler = s.mux
	if s.cors != nil {
		h = corsMiddleware(*s.cors)(h)
	}
	for i := len(s.chain) - 1; i >= 0; i-- {
		h = s.chain[i](h)
	}
	return Recovery(s.logger)(h)
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
	if rec.Header().Get("Content-Type") == "" {
		// no content-type set by handler is fine; nothing to assert.
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

// TestServerRecoveryAfterWriteKeepsOriginalStatus pins the post-write
// panic behaviour: once the handler has committed to a status (called
// WriteHeader or Write), Recovery cannot rewrite to 500 - net/http
// silently drops a second WriteHeader and would otherwise smear the
// recovery body across the in-flight response. The middleware must
// leave the committed status intact and log loudly instead.
func TestServerRecoveryAfterWriteKeepsOriginalStatus(t *testing.T) {
	s := newTestServer(t)
	s.HandleFunc("GET /half", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"partial":true`)) // intentional truncation
		panic("after write")
	})
	rec := httptest.NewRecorder()
	finalize(s).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/half", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("post-write panic must not rewrite status, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"partial":true`) {
		t.Errorf("expected partial body intact, got %q", rec.Body.String())
	}
}

// TestWriteValidationErrorSkipsPostCommit pins the M4 guard: when the
// response writer is already committed (some middleware wrote headers
// before the handler reached req.Validate()), WriteValidationError
// must NOT smear a 400 into the in-flight body - net/http would drop
// the WriteHeader and append the error text to whatever was already
// sent. The hook logs the dropped validation and leaves the wire alone.
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

// errBadField is a stand-in validator error used by the M4 guard test.
var errBadField = stringError("bad field")

type stringError string

func (e stringError) Error() string { return string(e) }

// TestWithLimitsTimeoutPanicReachesRecovery pins the M5 fix: a panic
// inside a `@timeout`-wrapped handler must still propagate to the
// outer Recovery middleware instead of being swallowed by goroutine
// isolation the way [http.TimeoutHandler] used to do. The handler
// panics immediately - well before the deadline - so the 500 must
// reach the client and the panic must be logged.
func TestWithLimitsTimeoutPanicReachesRecovery(t *testing.T) {
	logger := newTestServer(t).logger
	core := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("inside timeout")
	})
	guarded := WithLimits(core, Limits{Timeout: 100 * time.Millisecond})
	chain := Recovery(logger)(guarded)
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 from Recovery, got %d (body=%q)", rec.Code, rec.Body.String())
	}
}

// TestWithLimitsTimeoutContextCancellation verifies that a handler
// honouring ctx.Done() returns early when the deadline elapses. The
// handler waits on the context and writes a deterministic body so the
// assertion can confirm the cancel signal arrived.
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

func TestServerWithoutDefaultHealth(t *testing.T) {
	s := New(nil, WithoutDefaultHealth())
	rec := httptest.NewRecorder()
	finalize(s).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 when health disabled, got %d", rec.Code)
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

func TestRequestIDMiddlewareAddsHeader(t *testing.T) {
	s := newTestServer(t).Use(RequestID())
	captured := ""
	s.HandleFunc("GET /id", func(_ http.ResponseWriter, r *http.Request) {
		captured = RequestIDFromContext(r.Context())
	})
	rec := httptest.NewRecorder()
	finalize(s).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/id", nil))
	if rec.Header().Get("X-Request-Id") == "" {
		t.Error("missing X-Request-Id response header")
	}
	if captured == "" {
		t.Error("handler saw empty request ID")
	}
}

func TestRequestIDPassthrough(t *testing.T) {
	s := newTestServer(t).Use(RequestID())
	s.HandleFunc("GET /id", func(_ http.ResponseWriter, _ *http.Request) {})
	req := httptest.NewRequest(http.MethodGet, "/id", nil)
	req.Header.Set("X-Request-Id", "client-id-123")
	rec := httptest.NewRecorder()
	finalize(s).ServeHTTP(rec, req)
	if rec.Header().Get("X-Request-Id") != "client-id-123" {
		t.Errorf("expected client ID echoed back, got %q", rec.Header().Get("X-Request-Id"))
	}
}

func TestRequestIDFromMissingContext(t *testing.T) {
	if RequestIDFromContext(context.Background()) != "" {
		t.Error("expected empty string for missing ID")
	}
}

func TestAccessLogMiddleware(t *testing.T) {
	s := newTestServer(t).Use(AccessLog(s_logger(t))).Use(RequestID())
	s.HandleFunc("GET /a", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	rec := httptest.NewRecorder()
	finalize(s).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/a", nil))
	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d", rec.Code)
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
		// http.TimeoutHandler returns 503 by default
		t.Errorf("expected 503 from timeout, got %d", rec.Code)
	}
}

func TestServerSetters(t *testing.T) {
	s := newTestServer(t)
	s.SetDefaultReadTimeout(time.Second).
		SetDefaultWriteTimeout(2*time.Second).
		SetDefaultMaxBodySize(1024).
		SetDefaultMaxHeaderSize(8).
		SetJSONCodec(defaultCodec{}).
		SetLogger(s.Logger()).
		RegisterMiddleware("auth", func(h http.Handler) http.Handler { return h })
	if s.Codec() == nil || s.Logger() == nil {
		t.Error("codec/logger should be non-nil")
	}
	if s.Mux() == nil {
		t.Error("mux should be non-nil")
	}
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

	// Preflight.
	req = httptest.NewRequest(http.MethodOptions, "/c", nil)
	req.Header.Set("Origin", "https://app.example.com")
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

// markerCodec wraps the default codec but tags every encoded byte
// stream with a prefix so tests can prove the swap actually reaches
// generated handlers (or any caller that goes through [JSON]).
type markerCodec struct{ defaultCodec }

func (markerCodec) Encode(w io.Writer, v any) error {
	if _, err := w.Write([]byte("/*MARK*/")); err != nil {
		return err
	}
	return defaultCodec{}.Encode(w, v)
}

func TestGlobalJSONCodecSwapTakesEffect(t *testing.T) {
	t.Cleanup(func() { SetGlobalJSONCodec(defaultCodec{}) })
	SetGlobalJSONCodec(markerCodec{})
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

// s_logger returns a fresh Logger reused by middleware tests.
func s_logger(t *testing.T) Logger {
	t.Helper()
	return newTestServer(t).Logger()
}
