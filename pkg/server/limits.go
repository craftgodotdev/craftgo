package server

import (
	"context"
	"io"
	"net/http"
	"time"
)

// Limits holds the per-route guards [WithLimits] applies; a zero field applies none.
type Limits struct {
	// Timeout is a deadline on the request context only: the handler must watch ctx.Done().
	Timeout time.Duration

	// MaxBodySize caps the request body in bytes, as [BodyLimit] does.
	MaxBodySize int64
}

// WithLimits wraps h in the guards l sets, the timeout outermost so it covers reading the
// body. [Server.Handle] moves them outside the route's middlewares and skips the matching
// defaults.
func WithLimits(h http.Handler, l Limits) http.Handler {
	if l.MaxBodySize <= 0 && l.Timeout <= 0 {
		return h
	}
	return limitedHandler{Handler: l.guard(h), inner: h, limits: l}
}

// limitedHandler is a [WithLimits] handler: h in its guards, and h with the limits apart.
type limitedHandler struct {
	http.Handler
	inner  http.Handler
	limits Limits
}

// guard wraps h in the body cap (inner) and the timeout (outer) l sets.
func (l Limits) guard(h http.Handler) http.Handler {
	if l.MaxBodySize > 0 {
		h = maxBodySizeHandler(h, l.MaxBodySize)
	}
	if l.Timeout > 0 {
		h = timeoutHandler(h, l.Timeout)
	}
	return h
}

// limitsOf splits h into the handler and the limits it is guarded by: a [WithLimits] handler's
// own, and the server's defaults for those it leaves unset.
func (s *Server) limitsOf(h http.Handler) (http.Handler, Limits) {
	var l Limits
	if own, ok := h.(limitedHandler); ok {
		h, l = own.inner, own.limits
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if l.MaxBodySize <= 0 {
		l.MaxBodySize = s.defaultMaxBodySize
	}
	if l.Timeout <= 0 {
		l.Timeout = s.defaultHandlerTimeout
	}
	return h, l
}

// timeoutHandler runs h on the calling goroutine with a deadline of d on its context.
func timeoutHandler(h http.Handler, d time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), d)
		defer cancel()
		h.ServeHTTP(w, r.WithContext(ctx))
	})
}

// maxBodySizeHandler is the [BodyLimit] guard for n.
func maxBodySizeHandler(h http.Handler, n int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n > 0 && r.ContentLength > n {
			writeStatusError(w, http.StatusRequestEntityTooLarge)
			return
		}
		if r.Body != nil {
			r.Body = &cappedBody{ReadCloser: http.MaxBytesReader(w, r.Body, n)}
		}
		h.ServeHTTP(w, r)
	})
}

// cappedBody is a request body behind http.MaxBytesReader that remembers a read past its cap,
// which a multipart parser can report as a malformed part instead.
type cappedBody struct {
	io.ReadCloser
	over bool
}

func (b *cappedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if _, tooLarge := err.(*http.MaxBytesError); tooLarge {
		b.over = true
	}
	return n, err
}
