package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// CORSOptions configures [Server.SetCORS]; each field sets the matching Access-Control-*
// header. An AllowedOrigins entry is an origin, `*`, or a pattern with one `*` such as
// `https://*.example.com`.
type CORSOptions struct {
	AllowedOrigins      []string
	AllowedMethods      []string
	AllowedHeaders      []string
	ExposedHeaders      []string
	AllowCredentials    bool
	MaxAge              time.Duration
	AllowPrivateNetwork bool
}

// CORSPermissive is a development preset: any origin, the common methods, and the
// Content-Type and Authorization headers, without credentials.
func CORSPermissive() CORSOptions {
	return CORSOptions{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"Content-Type", "Authorization"},
	}
}

// CORSStrict allows only origin, GET and POST, and the Content-Type and Authorization
// headers, without credentials.
func CORSStrict(origin string) CORSOptions {
	return CORSOptions{
		AllowedOrigins: []string{origin},
		AllowedMethods: []string{"GET", "POST"},
		AllowedHeaders: []string{"Content-Type", "Authorization"},
	}
}

// corsMiddleware adds the CORS headers opts allows and answers a preflight from an allowed
// origin with 204.
func corsMiddleware(opts CORSOptions) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			allowed := matchOrigin(r.Header.Get("Origin"), opts.AllowedOrigins)
			if allowed == "" {
				next.ServeHTTP(w, r)
				return
			}
			setOriginHeaders(w.Header(), opts, allowed)
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				setPreflightHeaders(w.Header(), opts)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// setOriginHeaders sets the headers every response to an allowed origin carries.
func setOriginHeaders(h http.Header, opts CORSOptions, allowed string) {
	h.Set("Access-Control-Allow-Origin", allowed)
	if allowed != "*" {
		// The value echoes the request's Origin, so caches must key on it.
		h.Add("Vary", "Origin")
	}
	if opts.AllowCredentials {
		h.Set("Access-Control-Allow-Credentials", "true")
	}
	if len(opts.ExposedHeaders) > 0 {
		h.Set("Access-Control-Expose-Headers", strings.Join(opts.ExposedHeaders, ", "))
	}
}

// setPreflightHeaders sets the headers a preflight answer adds.
func setPreflightHeaders(h http.Header, opts CORSOptions) {
	if len(opts.AllowedMethods) > 0 {
		h.Set("Access-Control-Allow-Methods", strings.Join(opts.AllowedMethods, ", "))
	}
	if len(opts.AllowedHeaders) > 0 {
		h.Set("Access-Control-Allow-Headers", strings.Join(opts.AllowedHeaders, ", "))
	}
	if opts.MaxAge > 0 {
		h.Set("Access-Control-Max-Age", strconv.Itoa(int(opts.MaxAge.Seconds())))
	}
	if opts.AllowPrivateNetwork {
		h.Set("Access-Control-Allow-Private-Network", "true")
	}
}

// matchOrigin returns the Access-Control-Allow-Origin value for origin, or "" when no rule
// allows it.
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
	prefix, suffix, ok := strings.Cut(rule, "*")
	if !ok {
		return rule == origin
	}
	return strings.HasPrefix(origin, prefix) && strings.HasSuffix(origin, suffix) && len(origin) >= len(prefix)+len(suffix)
}
