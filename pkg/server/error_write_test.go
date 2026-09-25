package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// errDBDown is an error with no status.
var errDBDown = errors.New("dial tcp 10.0.0.5:5432: connection refused")

// An error without a status is logged with the trace ids and answered with an opaque 500.
func TestWriteError_UnknownErrorLogsWithTrace(t *testing.T) {
	logs := observeLogs(t)
	rec := httptest.NewRecorder()
	WriteError(rec, reqWithTrace(), errDBDown)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("unknown error must answer 500, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), errDBDown.Error()) {
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
	if got, _ := fields["error"].(string); got != errDBDown.Error() {
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
	WriteError(rec, req, errDBDown)

	if rec.Code != http.StatusTeapot {
		t.Errorf("custom handler should drive the status, got %d want 418", rec.Code)
	}
	if gotErr != errDBDown {
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

// An error returned after a Flush is logged and leaves the flushed event stream alone, with
// or without Compress in the chain.
func TestWriteErrorAfterFlushLeavesTheStream(t *testing.T) {
	for name, compress := range map[string]bool{"plain": false, "compressed": true} {
		t.Run(name, func(t *testing.T) {
			logs := observeLogs(t)
			s := New(nil)
			if compress {
				s.Use(Compress())
			}
			s.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.(http.Flusher).Flush()
				WriteError(w, r, errors.New("subscribe failed"))
			})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/events", nil)
			req.Header.Set("Accept-Encoding", "gzip")
			s.Handler().ServeHTTP(rec, req)
			res := rec.Result()
			if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "text/event-stream" || rec.Body.Len() != 0 {
				t.Errorf("flushed stream rewritten: status %d, Content-Type %q, body %q", res.StatusCode, res.Header.Get("Content-Type"), rec.Body.String())
			}
			if n := logs.FilterMessage("service error after response committed; not rewriting").Len(); n != 1 {
				t.Errorf("want the error logged as after-commit once, got %d lines", n)
			}
		})
	}
}

// emptyBodyError is a StatusError whose own MarshalJSON writes {}.
type emptyBodyError struct{ fakeStatusError }

func (emptyBodyError) MarshalJSON() ([]byte, error) { return []byte("{}"), nil }

// An error that marshals itself is written as it marshals, {} included; one
// that encodes to {} without marshaling itself gets the message envelope.
func TestWriteErrorKeepsASelfMarshaledBody(t *testing.T) {
	cases := map[string]struct {
		err  error
		want string
	}{
		"marshals itself":  {emptyBodyError{fakeStatusError{msg: "slow down", status: http.StatusTooManyRequests}}, `{}`},
		"encodes to empty": {fakeStatusError{msg: "slow down", status: http.StatusTooManyRequests}, `{"message":"slow down"}`},
	}
	for name, c := range cases {
		rec := httptest.NewRecorder()
		WriteError(rec, httptest.NewRequest(http.MethodGet, "/x", nil), c.err)
		if got := strings.TrimSpace(rec.Body.String()); got != c.want {
			t.Errorf("%s: body = %s, want %s", name, got, c.want)
		}
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

// pastDeadline returns a traced request whose own deadline has passed.
func pastDeadline(t *testing.T) *http.Request {
	t.Helper()
	req := reqWithTrace()
	ctx, cancel := context.WithDeadline(req.Context(), time.Now().Add(-time.Second))
	t.Cleanup(cancel)
	return req.WithContext(ctx)
}

// clientGone returns a request whose context is canceled, as a client that disconnects
// leaves it.
func clientGone() *http.Request {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(ctx)
}

// A deadline answers 504 {"message":"gateway timeout"}: the request's own, whatever context
// error the service returns, unlogged; a dependency's on a live request, logged at Warn with
// the trace ids.
func TestWriteErrorDeadline504(t *testing.T) {
	for name, tc := range map[string]struct {
		req  *http.Request
		err  error
		warn bool
	}{
		"own deadline":                    {pastDeadline(t), fmt.Errorf("query: %w", context.DeadlineExceeded), false},
		"canceled after the own deadline": {pastDeadline(t), fmt.Errorf("worker: %w", context.Canceled), false},
		"a dependency's deadline":         {reqWithTrace(), fmt.Errorf("payments api: %w", context.DeadlineExceeded), true},
	} {
		logs := observeLogs(t)
		rec := httptest.NewRecorder()
		WriteError(rec, tc.req, tc.err)
		if rec.Code != http.StatusGatewayTimeout || rec.Body.String() != `{"message":"gateway timeout"}`+"\n" {
			t.Errorf("%s: got %d %q, want the JSON 504", name, rec.Code, rec.Body.String())
		}
		if n := logs.FilterMessage("unhandled service error").Len(); n != 0 {
			t.Errorf("%s: logged as an unhandled service error", name)
		}
		warns := logs.FilterMessage("dependency deadline exceeded").All()
		if tc.warn != (len(warns) == 1) || logs.Len() != len(warns) {
			t.Errorf("%s: want the Warn line %v, got %v", name, tc.warn, logs.All())
			continue
		}
		if tc.warn {
			fields := warns[0].ContextMap()
			if warns[0].Level != zapcore.WarnLevel || fields["trace_id"] == nil || fields["error"] != tc.err.Error() {
				t.Errorf("%s: Warn line %v %v, want the trace ids and the error", name, warns[0].Level, fields)
			}
		}
	}
}

// A context error once the client has gone writes nothing and logs nothing.
func TestWriteErrorCanceledRequestWritesNothing(t *testing.T) {
	for name, err := range map[string]error{
		"canceled":            context.Canceled,
		"a deadline, wrapped": fmt.Errorf("db: %w", context.DeadlineExceeded),
	} {
		logs := observeLogs(t)
		rec := httptest.NewRecorder()
		tw := &trackingWriter{ResponseWriter: rec}
		WriteError(tw, clientGone(), err)
		if tw.Committed() || rec.Body.Len() != 0 || logs.Len() != 0 {
			t.Errorf("%s: committed %v, body %q, logs %v; want nothing", name, tw.Committed(), rec.Body.String(), logs.All())
		}
	}
}

// A context.Canceled on a request that is still live is the service's own and stays an
// unknown error: 500, logged.
func TestWriteErrorCanceledLiveRequestIsUnknown(t *testing.T) {
	logs := observeLogs(t)
	rec := httptest.NewRecorder()
	WriteError(rec, reqWithTrace(), fmt.Errorf("lookup: %w", context.Canceled))
	if rec.Code != http.StatusInternalServerError || logs.FilterMessage("unhandled service error").Len() != 1 {
		t.Errorf("got %d with logs %v, want 500 and the unhandled-error line", rec.Code, logs.All())
	}
}

// A deadline after the response is committed writes nothing and logs nothing.
func TestWriteErrorDeadlineAfterCommitWritesNothing(t *testing.T) {
	logs := observeLogs(t)
	rec := httptest.NewRecorder()
	tw := &trackingWriter{ResponseWriter: rec}
	tw.WriteHeader(http.StatusOK)
	WriteError(tw, pastDeadline(t), context.DeadlineExceeded)
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 || logs.Len() != 0 {
		t.Errorf("got %d %q with logs %v, want the 200 alone", rec.Code, rec.Body.String(), logs.All())
	}
}

// The SetHandleUnknownError handler never receives a context error WriteError answers.
func TestSetHandleUnknownErrorSkipsContextErrors(t *testing.T) {
	called := 0
	SetHandleUnknownError(func(w http.ResponseWriter, _ *http.Request, _ error) {
		called++
		w.WriteHeader(http.StatusTeapot)
	})
	t.Cleanup(func() { SetHandleUnknownError(nil) })
	observeLogs(t)
	WriteError(httptest.NewRecorder(), pastDeadline(t), context.DeadlineExceeded)
	WriteError(httptest.NewRecorder(), clientGone(), context.Canceled)
	WriteError(httptest.NewRecorder(), reqWithTrace(), context.DeadlineExceeded)
	if called != 0 {
		t.Errorf("the unknown-error handler ran %d times", called)
	}
}

// A handler past its route's timeout that returns the context's error answers 504 over a real
// connection.
func TestRouteTimeoutAnswers504(t *testing.T) {
	observeLogs(t)
	srv := New(nil)
	srv.Handle("GET /slow", WithLimits(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		WriteError(w, r, r.Context().Err())
	}), Limits{Timeout: 10 * time.Millisecond}))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	res, err := ts.Client().Get(ts.URL + "/slow")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusGatewayTimeout || string(body) != `{"message":"gateway timeout"}`+"\n" {
		t.Errorf("got %d %q, want the JSON 504", res.StatusCode, body)
	}
}
