package server

import (
	"context"
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
// body; [Server.Handle] then skips the matching defaults.
func WithLimits(h http.Handler, l Limits) http.Handler {
	if l.MaxBodySize > 0 {
		h = maxBodySizeHandler(h, l.MaxBodySize)
	}
	if l.Timeout > 0 {
		h = timeoutHandler(h, l.Timeout)
	}
	if l.MaxBodySize > 0 || l.Timeout > 0 {
		h = limitedHandler{Handler: h, bodyLimited: l.MaxBodySize > 0, timeoutSet: l.Timeout > 0}
	}
	return h
}

// limitedHandler marks a WithLimits handler with the guards it sets.
type limitedHandler struct {
	http.Handler
	bodyLimited bool
	timeoutSet  bool
}

// applyDefaults wraps h in the default body cap (inner) and handler timeout (outer), each
// unless h is a [WithLimits] handler that sets its own.
func (s *Server) applyDefaults(h http.Handler) http.Handler {
	s.mu.Lock()
	maxBody, timeout := s.defaultMaxBodySize, s.defaultHandlerTimeout
	s.mu.Unlock()
	own, _ := h.(limitedHandler)
	if maxBody > 0 && !own.bodyLimited {
		h = maxBodySizeHandler(h, maxBody)
	}
	if timeout > 0 && !own.timeoutSet {
		h = timeoutHandler(h, timeout)
	}
	return h
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
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, n)
		}
		h.ServeHTTP(w, r)
	})
}
