package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
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

// defaultValidationFailed answers 400 with err's text as the message, or only logs err once
// the response is committed.
func defaultValidationFailed(w http.ResponseWriter, r *http.Request, err error) {
	if responseCommitted(w) {
		ctx := context.Background()
		if r != nil {
			ctx = r.Context()
		}
		log.Default().WithContext(ctx).Error(
			"validation error after response committed; not rewriting",
			log.Err(err),
		)
		return
	}
	writeErrorMessage(w, http.StatusBadRequest, err.Error())
}

// writeErrorHead starts an error response with status: a JSON Content-Type, nosniff, and no
// Content-Length left from another body.
func writeErrorHead(w http.ResponseWriter, status int) {
	h := w.Header()
	h.Del("Content-Length")
	h.Set("Content-Type", contentTypeJSON)
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
}

// writeErrorMessage answers status with the JSON body {"message": msg}.
func writeErrorMessage(w http.ResponseWriter, status int, msg string) {
	writeErrorHead(w, status)
	_ = JSON().Encode(w, map[string]string{"message": msg})
}

// writeStatusError answers status with its status text, in lower case, as the message.
func writeStatusError(w http.ResponseWriter, status int) {
	writeErrorMessage(w, status, strings.ToLower(http.StatusText(status)))
}

// bodyTooLarge reports whether err is a read past a body cap, or r's body was read past its cap.
func bodyTooLarge(r *http.Request, err error) bool {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) || errors.Is(err, multipart.ErrMessageTooLarge) {
		return true
	}
	if r == nil {
		return false
	}
	capped, ok := r.Body.(*cappedBody)
	return ok && capped.over
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
// [WriteValidationError] calls. nil restores the default: 400 {"message": err's text}.
func SetDefaultValidationFailed(h ValidationFailedHandler) {
	if h == nil {
		h = defaultValidationFailed
	}
	validationFailed.Store(&h)
}

// WriteValidationError renders err with the [SetDefaultValidationFailed] handler; a read past
// a body cap is answered 413 {"message":"request entity too large"} without it.
func WriteValidationError(w http.ResponseWriter, r *http.Request, err error) {
	if bodyTooLarge(r, err) && !responseCommitted(w) {
		writeStatusError(w, http.StatusRequestEntityTooLarge)
		return
	}
	(*validationFailed.Load())(w, r, err)
}

// StatusError is an error that carries its HTTP status. [WriteError] writes its JSON encoding,
// or, when that fails, or is {} from an error that is no [json.Marshaler], its Error text as
// "message" and any ErrCode() as "code".
type StatusError interface {
	error
	HTTPStatus() int
}

// ResponseHeaderWriter is implemented by an error that sets response headers; [WriteError]
// calls it before writing a [StatusError]'s status.
type ResponseHeaderWriter interface {
	WriteResponseHeaders(http.ResponseWriter)
}

// UnknownErrorHandler renders an error [WriteError] has no answer of its own for; see
// [SetHandleUnknownError].
type UnknownErrorHandler func(w http.ResponseWriter, r *http.Request, err error)

// defaultUnknownError logs err with the request's trace ids and answers 500
// {"message":"internal server error"}, keeping err's text off the wire.
func defaultUnknownError(w http.ResponseWriter, r *http.Request, err error) {
	log.Default().WithContext(r.Context()).Error("unhandled service error", log.Err(err))
	writeStatusError(w, http.StatusInternalServerError)
}

// SetHandleUnknownError installs h, process-wide and safe while serving, for each error
// [WriteError] has no answer of its own for. nil restores the default: log err with the
// request's trace ids and answer 500 {"message":"internal server error"}.
func SetHandleUnknownError(h UnknownErrorHandler) {
	if h == nil {
		h = defaultUnknownError
	}
	unknownError.Store(&h)
}

// WriteError renders err: a [StatusError] in its chain, unlogged, as its status, headers and
// JSON body; a deadline as 504 {"message":"gateway timeout"}; a canceled request as nothing;
// anything else through the [SetHandleUnknownError] handler, or the log once committed.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var se StatusError
	isStatus := errors.As(err, &se)
	if !isStatus && writeContextError(w, r, err) {
		return
	}
	if responseCommitted(w) {
		log.Default().WithContext(r.Context()).Error(
			"service error after response committed; not rewriting",
			log.Err(err),
		)
		return
	}
	if !isStatus {
		(*unknownError.Load())(w, r, err)
		return
	}
	var hw ResponseHeaderWriter
	if errors.As(err, &hw) {
		hw.WriteResponseHeaders(w)
	}
	writeErrorHead(w, se.HTTPStatus())
	codec := JSON()
	var buf bytes.Buffer
	mErr := codec.Encode(&buf, se)
	_, selfMarshaled := se.(json.Marshaler)
	// Code generated before v1.10 leaves a body-less error's JSON to this {} fallback.
	if mErr != nil || (!selfMarshaled && strings.TrimSpace(buf.String()) == "{}") {
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

// writeContextError answers a context error, reporting whether it did: nothing when the request
// context is canceled, as by a gone client; 504 for a deadline, a dependency's logged at Warn.
func writeContextError(w http.ResponseWriter, r *http.Request, err error) bool {
	deadline := errors.Is(err, context.DeadlineExceeded)
	if !deadline && !errors.Is(err, context.Canceled) {
		return false
	}
	switch reqErr := r.Context().Err(); {
	case errors.Is(reqErr, context.Canceled):
		return true
	case reqErr == nil && !deadline:
		return false
	case reqErr == nil:
		log.Default().WithContext(r.Context()).Warn("dependency deadline exceeded", log.Err(err))
	}
	if !responseCommitted(w) {
		writeStatusError(w, http.StatusGatewayTimeout)
	}
	return true
}
