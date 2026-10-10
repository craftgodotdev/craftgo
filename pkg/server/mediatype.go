package server

import (
	"net/http"
	"strings"
)

// RequireJSONBody reports whether r's body may decode as JSON: its Content-Type is
// application/json or an application/*+json type, any parameters and letter case aside, or r
// carries none. Otherwise it answers 415 {"message":"unsupported media type"} and reports false,
// so a cross-site form or text/plain post never reaches a JSON handler.
func RequireJSONBody(w http.ResponseWriter, r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	if strings.TrimSpace(ct) == "" || isJSONMediaType(ct) {
		return true
	}
	writeStatusError(w, http.StatusUnsupportedMediaType)
	return false
}

// isJSONMediaType reports whether the media type of Content-Type value ct is application/json or
// an application/*+json type.
func isJSONMediaType(ct string) bool {
	mt, _, _ := strings.Cut(ct, ";")
	typ, sub, ok := strings.Cut(strings.ToLower(strings.TrimSpace(mt)), "/")
	return ok && typ == "application" && (sub == "json" || strings.HasSuffix(sub, "+json") && len(sub) > len("+json"))
}
