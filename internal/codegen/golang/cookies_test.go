package golang

import (
	"os"
	"path/filepath"
	"testing"
)

// cookieSrc declares a cookie of each kind: a value field taking the attributes @cookie names, a
// `cookie` field the logic sets in full, and an error's.
const cookieSrc = `package design
type LoginReq { user string }
type Session {
    token    string  @cookie("sid", maxAge: 24h, sameSite: strict)
    remember cookie? @cookie("remember", domain: "example.com")
    theme    string? @cookie("theme", httpOnly: false, secure: false, path: "/app")
}
error Unauthorized Locked {
    until cookie @cookie("lock", sameSite: none)
}
service Auth {
    @errors(Locked)
    post Login /login { request LoginReq  response Session }
}`

// A value field's cookie carries the attributes its @cookie declares, the safe defaults for the
// rest; a `cookie` field's goes out as the logic set it, under its declared name.
func TestGenerateTransportCookiesCarryTheirAttributes(t *testing.T) {
	pkg := analyze(t, cookieSrc)
	root := t.TempDir()
	if err := generateTransport(pkg, sampleConfig(), root, nil); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(root, "internal/transport/auth/login.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(out)
	mustParseGo(t, src)
	mustContainAll(t, src,
		`http.SetCookie(w, &http.Cookie{Name: "sid", Value: resp.Token, Path: "/", MaxAge: 86400, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})`,
		"if resp.Remember != nil {\n\t\t\t\t_c := *resp.Remember\n\t\t\t\t_c.Name = \"remember\"\n\t\t\t\thttp.SetCookie(w, &_c)\n\t\t\t}",
		"if resp.Theme != nil {\n\t\t\t\thttp.SetCookie(w, &http.Cookie{Name: \"theme\", Value: *resp.Theme, Path: \"/app\", SameSite: http.SameSiteLaxMode})\n\t\t\t}",
	)
}

// A `cookie` field is an *http.Cookie, and its type or error gains the constructor that fills in
// the declared attributes for the logic.
func TestGenerateCookieFieldsAndTheirConstructors(t *testing.T) {
	pkg := analyze(t, cookieSrc)
	dir := t.TempDir()
	if err := generateTypes(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	if err := generateErrors(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	types, _ := os.ReadFile(filepath.Join(dir, "design", "types.go"))
	mustParseGo(t, string(types))
	mustContainAll(t, collapseSpace(string(types)),
		"Remember *http.Cookie `json:\"-\" cookie:\"remember\"`",
		`func NewSessionRememberCookie(value string) *http.Cookie {`,
		`return &http.Cookie{Name: "remember", Value: value, Path: "/", Domain: "example.com", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode}`,
	)
	errs, _ := os.ReadFile(filepath.Join(dir, "design", "errors.go"))
	mustParseGo(t, string(errs))
	mustContainAll(t, string(errs),
		"if e.Until != nil {\n\t\t_c := *e.Until\n\t\t_c.Name = \"lock\"\n\t\tcs = append(cs, &_c)\n\t}",
		`func NewLockedErrUntilCookie(value string) *http.Cookie {`,
		`return &http.Cookie{Name: "lock", Value: value, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteNoneMode}`,
	)
}
