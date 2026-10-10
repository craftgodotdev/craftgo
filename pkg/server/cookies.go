package server

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
)

// CookieOptions are the attributes of the cookies a response's or an error's @cookie fields set.
type CookieOptions struct {
	// Path scopes each cookie; "" leaves it to the browser, which takes the request's directory.
	Path string
	// Domain shares each cookie with the subdomains of one; "" keeps it to the host that set it.
	Domain string
	// HTTPOnly hides each cookie from scripts.
	HTTPOnly bool
	// Secure sends each cookie over HTTPS only.
	Secure bool
	// SameSite is "lax", "strict" or "none", in any case, or "" to send no SameSite attribute.
	SameSite string
}

// DefaultCookieOptions returns the attributes cookies carry until [SetCookieOptions] changes them:
// Path "/", HttpOnly, Secure and SameSite=Lax.
func DefaultCookieOptions() CookieOptions {
	return CookieOptions{Path: "/", HTTPOnly: true, Secure: true, SameSite: "lax"}
}

// cookieAttrs holds the installed [CookieOptions], SameSite parsed.
type cookieAttrs struct {
	path, domain     string
	httpOnly, secure bool
	sameSite         http.SameSite
}

var cookieOptions atomic.Pointer[cookieAttrs]

func init() { _ = SetCookieOptions(DefaultCookieOptions()) }

// SetCookieOptions installs o, process-wide and safe while serving, as the attributes of the
// cookies generated handlers and errors set. It fails, changing nothing, on a SameSite other than
// lax, strict, none or "", and on SameSite=None without Secure, which browsers refuse.
func SetCookieOptions(o CookieOptions) error {
	a := &cookieAttrs{path: o.Path, domain: o.Domain, httpOnly: o.HTTPOnly, secure: o.Secure}
	switch strings.ToLower(strings.TrimSpace(o.SameSite)) {
	case "":
	case "lax":
		a.sameSite = http.SameSiteLaxMode
	case "strict":
		a.sameSite = http.SameSiteStrictMode
	case "none":
		if !o.Secure {
			return fmt.Errorf("sameSite none needs secure, which browsers require of it")
		}
		a.sameSite = http.SameSiteNoneMode
	default:
		return fmt.Errorf("sameSite %q is not lax, strict or none", o.SameSite)
	}
	cookieOptions.Store(a)
	return nil
}

// SetCookie adds a Set-Cookie header to w for name and value, with the attributes
// [SetCookieOptions] installed.
func SetCookie(w http.ResponseWriter, name, value string) {
	http.SetCookie(w, withCookieOptions(&http.Cookie{Name: name, Value: value}))
}

// withCookieOptions gives c the installed attributes and returns it.
func withCookieOptions(c *http.Cookie) *http.Cookie {
	a := cookieOptions.Load()
	c.Path, c.Domain, c.HttpOnly, c.Secure, c.SameSite = a.path, a.domain, a.httpOnly, a.secure, a.sameSite
	return c
}

// ResponseCookieSetter is implemented by an error whose @cookie fields set response cookies;
// [WriteError] sets each one, with the [SetCookieOptions] attributes, before a [StatusError]'s
// status.
type ResponseCookieSetter interface {
	ResponseCookies() []*http.Cookie
}
