// Hand-filled implementation. craftgo scaffolded this file once and
// will not overwrite it on subsequent gen runs.

package middleware

import (
	"encoding/json"
	"net/http"

	"github.com/dropship-dev/craftgo/pkg/server"
)

// NewAuthRequiredMiddleware returns the bearer-token gate. The
// expected header is captured by closure so each project can pin
// whichever token source it prefers (env, IdP, KMS).
func NewAuthRequiredMiddleware(expectedHeader string) server.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != expectedHeader {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"code":    "UNAUTHORIZED",
					"message": "missing or invalid bearer token",
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
