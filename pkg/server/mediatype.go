package server

import (
	"net/http"
	"strings"
)

// RequireJSONBody reports whether r's body may decode as JSON: its Content-Type is
// application/json or an application/*+json type, any parameters and letter case aside, or r
// carries neither a Content-Type nor a body, which the decoder then reports missing. Otherwise it
// answers 415 {"message":"unsupported media type"} and reports false, so neither a cross-site
// form or text/plain post nor an untyped Blob or ArrayBuffer body, which a cross-site fetch also
// sends without a preflight, reaches a JSON handler.
func RequireJSONBody(w http.ResponseWriter, r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	if isJSONMediaType(ct) || strings.TrimSpace(ct) == "" && withoutBody(r) {
		return true
	}
	writeStatusError(w, http.StatusUnsupportedMediaType)
	return false
}

// withoutBody reports whether r came without a body.
func withoutBody(r *http.Request) bool {
	return r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0
}

// isJSONMediaType reports whether the media type of Content-Type value ct is application/json or
// an application/*+json type.
func isJSONMediaType(ct string) bool {
	mt, _, _ := strings.Cut(ct, ";")
	typ, sub, ok := strings.Cut(strings.ToLower(strings.TrimSpace(mt)), "/")
	return ok && typ == "application" && (sub == "json" || strings.HasSuffix(sub, "+json") && len(sub) > len("+json"))
}
