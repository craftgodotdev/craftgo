package server

import (
	"bytes"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/craftgodotdev/craftgo/pkg/log"
)

// ValidationFailedHandler is the function shape every generated
// handler calls when `req.Validate()` returns a non-nil error. The
// default implementation mirrors `http.Error` - a 400 with the
// validator's message - and applications swap it in by calling
// [SetDefaultValidationFailed] once at startup.
type ValidationFailedHandler func(w http.ResponseWriter, r *http.Request, err error)

// validationFailed and unknownError hold the live error-rendering handlers.
// atomic.Value keeps reads allocation-free on the hot path; writes happen at
// most once per process (init, then any Set* call at startup).
var (
	validationFailed atomic.Value // ValidationFailedHandler
	unknownError     atomic.Value // UnknownErrorHandler
)

func init() {
	validationFailed.Store(ValidationFailedHandler(defaultValidationFailed))
	unknownError.Store(UnknownErrorHandler(defaultUnknownError))
}

// defaultValidationFailed writes the validator's message body with a
// 400 Bad Request status. This is the wire default;
// [SetDefaultValidationFailed] swaps it.
//
// When the response is already committed (some middleware wrote
// headers before the handler ran) the 400 cannot be written - net/http
// silently drops a second WriteHeader call. The hook logs the dropped
// validation error so the operator notices the bad ordering instead of
// having it vanish, then returns without touching the wire to avoid
// corrupting the in-flight body.
func defaultValidationFailed(w http.ResponseWriter, r *http.Request, err error) {
	if c, ok := w.(interface{ Committed() bool }); ok && c.Committed() {
		log.Default().WithContext(r.Context()).Error(
			"validation error after response committed; not rewriting",
			log.Err(err),
		)
		return
	}
	http.Error(w, err.Error(), http.StatusBadRequest)
}

// SetDefaultValidationFailed installs a process-wide handler invoked
// for every `req.Validate()` failure. Pass nil to revert to the
// default. The function is safe to call concurrently with handler
// dispatch; a single-pointer atomic swap keeps the hot path lock-free.
func SetDefaultValidationFailed(h ValidationFailedHandler) {
	if h == nil {
		h = defaultValidationFailed
	}
	validationFailed.Store(h)
}

// WriteValidationError is the indirection generated handlers call.
// Kept exported so the codegen template can name it without
// reflection; not intended for application use.
func WriteValidationError(w http.ResponseWriter, r *http.Request, err error) {
	validationFailed.Load().(ValidationFailedHandler)(w, r, err)
}

// SetHandleNotFound installs a per-server handler invoked for
// requests whose path does not match any registered route. Replaces
// the stdlib mux's default `404 page not found` body. Pass nil to
// fall back to the default.
//
// Health endpoints, static handlers, and middleware-rejected requests
// still bypass this handler - only routes that reach the mux without
// a match trigger it.
func (s *Server) SetHandleNotFound(h http.Handler) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notFound = h
	return s
}

// StatusError is the contract every craftgo-generated typed error satisfies:
// a standard error that also reports an HTTP status. [WriteError] renders these
// directly. Application code can implement it on its own error types to have
// them rendered with a chosen status instead of falling through to the
// unknown-error handler.
type StatusError interface {
	error
	HTTPStatus() int
}

// ResponseHeaderWriter is the optional extension a typed error implements when
// it declares `@header` / `@cookie` fields. [WriteError] calls it before the
// status line so those values reach the wire ahead of the JSON body.
type ResponseHeaderWriter interface {
	WriteResponseHeaders(http.ResponseWriter)
}

// UnknownErrorHandler renders an error that is NOT a recognised [StatusError] -
// a bare errors.New / fmt.Errorf returned by service logic that carries no HTTP
// status. It receives the request so it can read trace context for logging. The
// default logs the error with the request's trace IDs and responds 500 with a
// `{"message": ...}` body; [SetHandleUnknownError] swaps it process-wide.
type UnknownErrorHandler func(w http.ResponseWriter, r *http.Request, err error)

// defaultUnknownError is the wire default for errors without an HTTP status. An
// unknown error is an unhandled failure, so it is logged at Error level with
// the request's trace context (trace_id / span_id / request_id ride the line
// via WithContext) before a 500 with the error text as the JSON message body is
// written - operators can find the failure by trace, and the client still gets
// a discriminable response.
func defaultUnknownError(w http.ResponseWriter, r *http.Request, err error) {
	log.Default().WithContext(r.Context()).Error("unhandled service error", log.Err(err))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	_ = JSON().Encode(w, map[string]string{"message": err.Error()})
}

// SetHandleUnknownError installs a process-wide handler for service errors that
// are not craftgo typed errors (no HTTPStatus) - use it to map a domain error
// to a status, redact, or return a uniform envelope. Pass nil to revert to the
// default (log with trace context + 500 + message). Safe to call concurrently
// with dispatch; a single-pointer atomic swap keeps the hot path lock-free.
func SetHandleUnknownError(h UnknownErrorHandler) {
	if h == nil {
		h = defaultUnknownError
	}
	unknownError.Store(h)
}

// WriteError is the indirection generated handlers call when service logic
// returns a non-nil error. It splits on whether the error is a recognised
// craftgo typed error:
//
//   - a [StatusError] is rendered directly from its interface — the declared
//     HTTP status, the optional `@header`/`@cookie` writes via
//     [ResponseHeaderWriter], then a JSON body: the codec encodes the error's
//     declared body struct, or — when the error declares no body and would
//     marshal to `{}` — a `{code, message}` envelope built from `ErrCode()` /
//     `Error()` so clients can still discriminate the failure. A typed error is
//     an expected outcome (a declared 4xx/5xx), so it is NOT logged;
//   - anything else (a bare errors.New / fmt.Errorf) is delegated to the
//     [SetHandleUnknownError] handler, whose default logs the error with the
//     request's trace context and responds 500.
//
// Header precedence: WriteResponseHeaders writes user-declared fields FIRST,
// then the framework stamps `Content-Type: application/json; charset=utf-8`
// LAST, so a `@header("Content-Type")` field is overridden - intentional, since
// the body that follows is always JSON. Use a passthrough handler for a
// different content type.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	se, ok := err.(StatusError)
	if !ok {
		unknownError.Load().(UnknownErrorHandler)(w, r, err)
		return
	}
	if hw, ok := err.(ResponseHeaderWriter); ok {
		hw.WriteResponseHeaders(w)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(se.HTTPStatus())
	codec := JSON()
	var buf bytes.Buffer
	if mErr := codec.Encode(&buf, err); mErr != nil || strings.TrimSpace(buf.String()) == "{}" {
		env := map[string]string{"message": err.Error()}
		if c, ok := err.(interface{ ErrCode() string }); ok {
			env["code"] = c.ErrCode()
		}
		_ = codec.Encode(w, env)
		return
	}
	w.Write(buf.Bytes())
}
