package middleware

import (
	"net/http"

	"github.com/craftgodotdev/craftgo/pkg/server"
)

// NewRequestStampMiddleware returns a middleware that adds an
// `X-Request-Stamp` response header. Used by the e2e suite to confirm
// a SECOND middleware in the chain runs in addition to AuthRequired.
func NewRequestStampMiddleware() server.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Request-Stamp", "stamped")
			next.ServeHTTP(w, r)
		})
	}
}
