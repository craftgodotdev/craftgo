package middleware

import (
	"encoding/json"
	"net/http"

	"github.com/craftgodotdev/craftgo/pkg/server"
)

// NewProfileAuthMiddleware returns the bearer-token check declared by the DSL's
// `middleware ProfileAuth`. Wire it like:
//
//	svc.ProfileAuth = middleware.NewProfileAuthMiddleware("Bearer secret-token")
func NewProfileAuthMiddleware(expectedAuthHeader string) server.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != expectedAuthHeader {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]string{"code": "UNAUTHORIZED", "message": "missing or invalid bearer token"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
