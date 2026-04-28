package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// CORSOptions configures the CORS middleware. Most fields mirror the
// corresponding HTTP headers; AllowedOrigins entries may use a single
// leading wildcard (`https://*.example.com`) or the full wildcard `*`.
type CORSOptions struct {
	AllowedOrigins      []string
	AllowedMethods      []string
	AllowedHeaders      []string
	ExposedHeaders      []string
	AllowCredentials    bool
	MaxAge              time.Duration
	AllowPrivateNetwork bool
}

// CORSPermissive returns a development-mode preset that mirrors browser
// defaults for non-credentialed APIs. Not suitable for production.
func CORSPermissive() CORSOptions {
	return CORSOptions{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"Content-Type", "Authorization"},
	}
}

// CORSStrict returns a production-leaning preset locked to a single origin
// and a small set of common headers; toggle credentials on at the call
// site if needed.
func CORSStrict(origin string) CORSOptions {
	return CORSOptions{
		AllowedOrigins: []string{origin},
		AllowedMethods: []string{"GET", "POST"},
		AllowedHeaders: []string{"Content-Type", "Authorization"},
	}
}

// corsMiddleware applies opts to every request and short-circuits OPTIONS
// preflights with the matching Access-Control-* response headers.
func corsMiddleware(opts CORSOptions) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if allowed := matchOrigin(origin, opts.AllowedOrigins); allowed != "" {
				w.Header().Set("Access-Control-Allow-Origin", allowed)
				if opts.AllowCredentials {
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				}
				if len(opts.ExposedHeaders) > 0 {
					w.Header().Set("Access-Control-Expose-Headers", strings.Join(opts.ExposedHeaders, ", "))
				}
			}
			if r.Method == http.MethodOptions && origin != "" {
				if len(opts.AllowedMethods) > 0 {
					w.Header().Set("Access-Control-Allow-Methods", strings.Join(opts.AllowedMethods, ", "))
				}
				if len(opts.AllowedHeaders) > 0 {
					w.Header().Set("Access-Control-Allow-Headers", strings.Join(opts.AllowedHeaders, ", "))
				}
				if opts.MaxAge > 0 {
					w.Header().Set("Access-Control-Max-Age", strconv.Itoa(int(opts.MaxAge.Seconds())))
				}
				if opts.AllowPrivateNetwork {
					w.Header().Set("Access-Control-Allow-Private-Network", "true")
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// matchOrigin returns the Allow-Origin value that should be sent to a
// client whose Origin header is `origin`. Returns "" when no rule matches
// so the middleware can omit the header entirely.
func matchOrigin(origin string, allowed []string) string {
	if origin == "" {
		return ""
	}
	for _, rule := range allowed {
		switch {
		case rule == "*":
			return "*"
		case rule == origin:
			return origin
		case strings.Contains(rule, "*"):
			if matchWildcard(rule, origin) {
				return origin
			}
		}
	}
	return ""
}

// matchWildcard reports whether origin matches a single-wildcard rule like
// `https://*.example.com`.
func matchWildcard(rule, origin string) bool {
	idx := strings.Index(rule, "*")
	if idx < 0 {
		return rule == origin
	}
	prefix := rule[:idx]
	suffix := rule[idx+1:]
	return strings.HasPrefix(origin, prefix) && strings.HasSuffix(origin, suffix) && len(origin) >= len(prefix)+len(suffix)
}
