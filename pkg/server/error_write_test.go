package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/craftgodotdev/craftgo/pkg/log"
)

// fakeStatusError is a stand-in for a craftgo-generated typed error: it carries
// an HTTP status, so WriteError renders it directly instead of treating it as
// an unknown error.
type fakeStatusError struct {
	msg    string
	status int
}

func (e fakeStatusError) Error() string   { return e.msg }
func (e fakeStatusError) HTTPStatus() int { return e.status }

// observeLogs swaps the package logger for one that records into an observer,
// restoring the previous default on cleanup. Returns the recorded logs.
func observeLogs(t *testing.T) *observer.ObservedLogs {
	t.Helper()
	core, logs := observer.New(zapcore.InfoLevel)
	prev := log.Default()
	log.SetDefault(log.NewZap(zap.New(core)))
	t.Cleanup(func() { log.SetDefault(prev) })
	return logs
}

// reqWithTrace returns a request whose context carries a valid OTel span, so
// WithContext stamps trace_id / span_id onto any log line.
func reqWithTrace() *http.Request {
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10},
		SpanID:     trace.SpanID{0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18},
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)
	return httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(ctx)
}

// TestWriteError_TypedErrorRendersStatusNoLog pins that a recognised typed
// error (a StatusError) is rendered with its own HTTP status and is NOT logged
// - a declared 4xx/5xx is an expected outcome, not an operational failure.
func TestWriteError_TypedErrorRendersStatusNoLog(t *testing.T) {
	logs := observeLogs(t)
	rec := httptest.NewRecorder()
	WriteError(rec, reqWithTrace(), fakeStatusError{msg: "duplicate", status: http.StatusConflict})

	if rec.Code != http.StatusConflict {
		t.Errorf("typed error must render its own status, got %d want 409", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "duplicate") {
		t.Errorf("expected message in body, got %q", rec.Body.String())
	}
	if n := logs.FilterMessage("unhandled service error").Len(); n != 0 {
		t.Errorf("typed error must not be logged as unhandled, got %d log entries", n)
	}
}

// TestWriteError_UnknownErrorLogsWithTrace pins the contract: an error that is
// NOT a typed StatusError (a bare fmt.Errorf) is logged at Error level with the
// request's trace context (trace_id / span_id) and answered 500 with an OPAQUE
// body — the raw error text stays in the log and never leaks to the client.
func TestWriteError_UnknownErrorLogsWithTrace(t *testing.T) {
	logs := observeLogs(t)
	rec := httptest.NewRecorder()
	WriteError(rec, reqWithTrace(), context.DeadlineExceeded)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("unknown error must answer 500, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), context.DeadlineExceeded.Error()) {
		t.Errorf("raw error text must NOT leak into the 500 body, got %q", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "internal server error") {
		t.Errorf("500 body should carry the opaque message, got %q", rec.Body.String())
	}
	entries := logs.FilterMessage("unhandled service error").All()
	if len(entries) != 1 {
		t.Fatalf("unknown error must be logged once, got %d entries", len(entries))
	}
	fields := entries[0].ContextMap()
	if _, ok := fields["trace_id"]; !ok {
		t.Errorf("log line must carry trace_id; fields: %v", fields)
	}
	if _, ok := fields["span_id"]; !ok {
		t.Errorf("log line must carry span_id; fields: %v", fields)
	}
	// The full error must reach the LOG (it is kept off the wire).
	if got, _ := fields["error"].(string); got != context.DeadlineExceeded.Error() {
		t.Errorf("log line must carry the real error; got error=%q", got)
	}
}

// TestSetHandleUnknownError_Swaps pins that the hook replaces the default and
// receives (w, r, err) - so an app can map domain errors, log differently, or
// return a uniform envelope. Reverting with nil restores the default.
func TestSetHandleUnknownError_Swaps(t *testing.T) {
	var gotErr error
	var gotReq *http.Request
	SetHandleUnknownError(func(w http.ResponseWriter, r *http.Request, err error) {
		gotErr, gotReq = err, r
		w.WriteHeader(http.StatusTeapot)
	})
	t.Cleanup(func() { SetHandleUnknownError(nil) })

	rec := httptest.NewRecorder()
	req := reqWithTrace()
	WriteError(rec, req, context.DeadlineExceeded)

	if rec.Code != http.StatusTeapot {
		t.Errorf("custom handler should drive the status, got %d want 418", rec.Code)
	}
	if gotErr != context.DeadlineExceeded {
		t.Errorf("custom handler did not receive the error, got %v", gotErr)
	}
	if gotReq != req {
		t.Error("custom handler did not receive the request (needed for trace context)")
	}

	// A typed error still bypasses the unknown hook entirely.
	gotErr = nil
	rec2 := httptest.NewRecorder()
	WriteError(rec2, reqWithTrace(), fakeStatusError{msg: "x", status: http.StatusNotFound})
	if rec2.Code != http.StatusNotFound {
		t.Errorf("typed error must bypass the unknown hook, got %d", rec2.Code)
	}
	if gotErr != nil {
		t.Error("typed error must not reach the unknown-error hook")
	}
}
