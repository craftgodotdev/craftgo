// Hand-written impl. The codegen scaffolds this file once and never
// overwrites — the bearer-token logic below survives subsequent gen
// runs.

package middleware

import (
	"encoding/json"
	"net/http"

	"github.com/craftgodotdev/craftgo/pkg/server"
)

// NewAuthRequiredMiddleware returns the bearer-token check declared by
// the DSL's `middleware AuthRequired`. The signature is owned by the
// project — this version takes the expected token directly so callers
// can pin it in main.go without reaching into the rest of the
// codebase. Wire it like:
//
//	svc.AuthRequired = middleware.NewAuthRequiredMiddleware("Bearer secret-token")
func NewAuthRequiredMiddleware(expectedAuthHeader string) server.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != expectedAuthHeader {
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
