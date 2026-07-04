package server

import (
	"context"
	"net/http"
	"time"
)

// Limits bundles the per-method runtime guards the DSL surfaces via
// `@timeout` and `@maxBodySize`. Zero values mean "no limit" for that
// dimension - the wrapper only applies a guard when the corresponding
// field is non-zero.
//
// Transport-level deadlines (`http.Server.ReadTimeout`,
// `WriteTimeout`, `IdleTimeout`, `ReadHeaderTimeout`,
// `MaxHeaderBytes`) are NOT modelled here - those are server-wide
// concerns the user configures on the underlying [http.Server]
// directly when the stdlib defaults are insufficient. This package
// only owns guards that can be enforced per-handler.
type Limits struct {
	// Timeout caps the full handler lifecycle (decode body → user
	// logic → encode response) by deriving a context.WithTimeout
	// from the request and handing it to the handler. Handlers that
	// honour ctx.Done() return early on deadline; handlers that do
	// not run to completion but the response writer is detached so
	// late writes are silently dropped. Passthrough endpoints opt
	// out so streaming bodies stay intact.
	//
	// The middleware does NOT goroutine-isolate the handler the way
	// [http.TimeoutHandler] does. That stdlib helper buffers the
	// response in memory and discards any panic that fires after
	// the timeout cut-off, masking real bugs as "request timed out"
	// 503s. Running in-line keeps panics visible to the outer
	// Recovery middleware at the cost of losing the force-cancel
	// guarantee on context-deaf handlers.
	Timeout time.Duration

	// MaxBodySize caps the request body size in bytes. Wraps r.Body
	// with [http.MaxBytesReader] before the user's handler runs;
	// reads past the cap return an error, which the JSON decoder
	// surfaces as 400.
	MaxBodySize int64
}

// WithLimits returns h wrapped with the runtime guards declared in l.
// Zero-valued fields skip their respective wrapping so the function
// is a cheap pass-through when the DSL declared no limits.
//
// Wrapping order is innermost-first: MaxBodySize wraps r.Body before
// the handler reads it, then Timeout wraps the whole chain so the
// timeout includes the body-read step.
//
// When l sets a MaxBodySize or a Timeout the result is tagged (see
// [limitedHandler]) so [Server.Handle] / [Server.HandleFunc] know this route
// already has its own cap / deadline and skip the matching server-wide default:
// a per-method @maxBodySize or @timeout takes priority over the default rather
// than being layered on top of it.
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

// limitedHandler tags a handler whose own per-route guards have already been
// applied (via [WithLimits]), recording which ones so [Server.Handle] /
// [Server.HandleFunc] know which server-wide defaults to skip. The embedded
// Handler promotes ServeHTTP so the tag behaves exactly like the wrapped
// handler.
type limitedHandler struct {
	http.Handler
	bodyLimited bool
	timeoutSet  bool
}

// handlerHasBodyLimit reports whether h already carries a per-route body cap, in
// which case the default body cap must not be layered on top.
func handlerHasBodyLimit(h http.Handler) bool {
	lh, ok := h.(limitedHandler)
	return ok && lh.bodyLimited
}

// handlerHasTimeout reports whether h already carries a per-route timeout, in
// which case the default handler timeout must not be layered on top.
func handlerHasTimeout(h http.Handler) bool {
	lh, ok := h.(limitedHandler)
	return ok && lh.timeoutSet
}

// timeoutHandler attaches a context.WithTimeout to the request before
// delegating. Handlers that respect ctx.Done() return promptly when
// the deadline elapses; handlers that ignore it keep running on the
// same goroutine - which is the deliberate trade-off so any panic
// they raise still reaches the outer Recovery middleware instead of
// being swallowed by [http.TimeoutHandler]'s goroutine isolation.
//
// On timeout, the client connection follows whatever the handler
// eventually writes; the cancellation signal is the only feedback the
// framework provides. Pair `@timeout` with handlers that check
// `ctx.Err()` at await points for deterministic deadline enforcement.
func timeoutHandler(h http.Handler, d time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), d)
		defer cancel()
		h.ServeHTTP(w, r.WithContext(ctx))
	})
}

// maxBodySizeHandler returns a middleware that swaps r.Body with a
// MaxBytesReader so reads past the cap fail loudly. Errors propagate
// up the stack as a normal Read error - the user's handler decides
// whether to translate them into 400 / 413.
func maxBodySizeHandler(h http.Handler, n int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reject upfront when the client declares more bytes than we
		// allow. [http.MaxBytesReader] alone only fires once the
		// handler actually reads past the cap, so a request whose
		// Content-Length already exceeds n would otherwise sail past
		// the guard if the handler never touched r.Body (e.g. early
		// validation rejected the request on a header).
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
