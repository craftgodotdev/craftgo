package middleware

import (
	"net/http"

	"github.com/craftgodotdev/craftgo/pkg/server"
)

// NewRequestStampMiddleware adds an X-Request-Stamp response header so a test
// can confirm a SECOND middleware runs alongside ProfileAuth.
func NewRequestStampMiddleware() server.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Request-Stamp", "stamped")
			next.ServeHTTP(w, r)
		})
	}
}
