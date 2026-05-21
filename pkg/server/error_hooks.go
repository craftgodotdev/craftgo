package server

import (
	"net/http"
	"sync/atomic"

	"github.com/craftgodotdev/craftgo/pkg/log"
)

// ValidationFailedHandler is the function shape every generated
// handler calls when `req.Validate()` returns a non-nil error. The
// default implementation mirrors `http.Error` - a 400 with the
// validator's message - and applications swap it in by calling
// [SetDefaultValidationFailed] once at startup.
type ValidationFailedHandler func(w http.ResponseWriter, r *http.Request, err error)

// validationFailed holds the live handler. atomic.Value keeps reads
// allocation-free on the hot path; writes happen at most once per
// process (during init).
var validationFailed atomic.Value

func init() {
	validationFailed.Store(ValidationFailedHandler(defaultValidationFailed))
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
