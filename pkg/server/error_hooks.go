package server

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/craftgodotdev/craftgo/pkg/log"
)

// ValidationFailedHandler renders a request that failed binding or validation; see
// [SetDefaultValidationFailed].
type ValidationFailedHandler func(w http.ResponseWriter, r *http.Request, err error)

var (
	validationFailed atomic.Pointer[ValidationFailedHandler]
	unknownError     atomic.Pointer[UnknownErrorHandler]
)

func init() {
	SetDefaultValidationFailed(nil)
	SetHandleUnknownError(nil)
}

// defaultValidationFailed answers 400 with err's text, or only logs err once the response
// is committed.
func defaultValidationFailed(w http.ResponseWriter, r *http.Request, err error) {
	if responseCommitted(w) {
		log.Default().WithContext(r.Context()).Error(
			"validation error after response committed; not rewriting",
			log.Err(err),
		)
		return
	}
	http.Error(w, err.Error(), http.StatusBadRequest)
}

// responseCommitted asks the first writer on w's Unwrap chain that has a Committed method.
func responseCommitted(w http.ResponseWriter) bool {
	for w != nil {
		if c, ok := w.(interface{ Committed() bool }); ok {
			return c.Committed()
		}
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return false
		}
		w = u.Unwrap()
	}
	return false
}

// SetDefaultValidationFailed installs h, process-wide and safe while serving, as the handler
// [WriteValidationError] calls. nil restores the default: 400 text/plain with err's text.
func SetDefaultValidationFailed(h ValidationFailedHandler) {
	if h == nil {
		h = defaultValidationFailed
	}
	validationFailed.Store(&h)
}

// WriteValidationError renders err with the [SetDefaultValidationFailed] handler.
func WriteValidationError(w http.ResponseWriter, r *http.Request, err error) {
	(*validationFailed.Load())(w, r, err)
}

// StatusError is an error that carries its HTTP status. [WriteError] writes its JSON encoding,
// or, when that fails or is {}, its Error text as "message" and any ErrCode() as "code".
type StatusError interface {
	error
	HTTPStatus() int
}

// ResponseHeaderWriter is implemented by an error that sets response headers; [WriteError]
// calls it before writing a [StatusError]'s status.
type ResponseHeaderWriter interface {
	WriteResponseHeaders(http.ResponseWriter)
}

// UnknownErrorHandler renders an error with no [StatusError] in its chain; see
// [SetHandleUnknownError].
type UnknownErrorHandler func(w http.ResponseWriter, r *http.Request, err error)

// defaultUnknownError logs err with the request's trace ids and answers 500
// {"message":"internal server error"}, keeping err's text off the wire.
func defaultUnknownError(w http.ResponseWriter, r *http.Request, err error) {
	log.Default().WithContext(r.Context()).Error("unhandled service error", log.Err(err))
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusInternalServerError)
	_ = JSON().Encode(w, map[string]string{"message": "internal server error"})
}

// SetHandleUnknownError installs h, process-wide and safe while serving, for errors with no
// [StatusError] in their chain. nil restores the default: log err with the request's trace
// ids and answer 500 {"message":"internal server error"}.
func SetHandleUnknownError(h UnknownErrorHandler) {
	if h == nil {
		h = defaultUnknownError
	}
	unknownError.Store(&h)
}

// WriteError renders err: a [StatusError] in its chain, unlogged, as its status, its
// [ResponseHeaderWriter] headers and a JSON body (Content-Type forced); any other error through
// the [SetHandleUnknownError] handler. Once the response is committed, err is only logged.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	if responseCommitted(w) {
		log.Default().WithContext(r.Context()).Error(
			"service error after response committed; not rewriting",
			log.Err(err),
		)
		return
	}
	var se StatusError
	if !errors.As(err, &se) {
		(*unknownError.Load())(w, r, err)
		return
	}
	var hw ResponseHeaderWriter
	if errors.As(err, &hw) {
		hw.WriteResponseHeaders(w)
	}
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(se.HTTPStatus())
	codec := JSON()
	var buf bytes.Buffer
	if mErr := codec.Encode(&buf, se); mErr != nil || strings.TrimSpace(buf.String()) == "{}" {
		env := map[string]string{"message": se.Error()}
		var coded interface{ ErrCode() string }
		if errors.As(err, &coded) {
			env["code"] = coded.ErrCode()
		}
		_ = codec.Encode(w, env)
		return
	}
	_, _ = w.Write(buf.Bytes())
}
