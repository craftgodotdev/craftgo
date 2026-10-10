package server

import "net/http"

// ResponseCookieSetter is implemented by an error whose @cookie fields set response cookies;
// [WriteError] sets those of the [StatusError] that sets the status, as the error returns them,
// before it writes that status.
type ResponseCookieSetter interface {
	ResponseCookies() []*http.Cookie
}
