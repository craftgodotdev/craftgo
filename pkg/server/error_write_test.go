package server

import (
	"context"
	"fmt"
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

// fakeStatusError is a StatusError with a fixed message and status.
type fakeStatusError struct {
	msg    string
	status int
}

func (e fakeStatusError) Error() string   { return e.msg }
func (e fakeStatusError) HTTPStatus() int { return e.status }

// observeLogs points log.Default at an observer until the test ends.
func observeLogs(t *testing.T) *observer.ObservedLogs {
	t.Helper()
	core, logs := observer.New(zapcore.InfoLevel)
	prev := log.Default()
	log.SetDefault(log.NewZap(zap.New(core)))
	t.Cleanup(func() { log.SetDefault(prev) })
	return logs
}

// reqWithTrace returns a request whose context carries a valid span.
func reqWithTrace() *http.Request {
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10},
		SpanID:     trace.SpanID{0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18},
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)
	return httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(ctx)
}

// A StatusError is written with its own status and not logged.
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

// An error without a status is logged with the trace ids and answered with an opaque 500.
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
	// The log carries the real error.
	if got, _ := fields["error"].(string); got != context.DeadlineExceeded.Error() {
		t.Errorf("log line must carry the real error; got error=%q", got)
	}
}

// SetHandleUnknownError's handler receives every error without a status, with its request.
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

	// A StatusError never reaches the hook.
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

// A StatusError wrapped with %w keeps its status and message and is not logged.
func TestWriteErrorUnwrapsTypedErrors(t *testing.T) {
	logs := observeLogs(t)
	rec := httptest.NewRecorder()
	err := fmt.Errorf("charge card: %w", fakeStatusError{msg: "card declined", status: http.StatusPaymentRequired})
	WriteError(rec, httptest.NewRequest(http.MethodPost, "/pay", nil), err)
	if rec.Code != http.StatusPaymentRequired {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusPaymentRequired)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"message":"card declined"`) {
		t.Errorf("body = %q, want the typed error's message", body)
	}
	if logs.Len() != 0 {
		t.Errorf("a typed error must not be logged, got %v", logs.All())
	}
}
