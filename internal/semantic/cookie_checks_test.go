package semantic

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// A response's and an error's cookies take the attributes @cookie names, after or instead of the
// cookie's name, and a `cookie` field under @cookie.
func TestCookieAttributesAndCookieFieldsAreAccepted(t *testing.T) {
	mustClean(t, `type LoginReq { user string  theme string? @cookie("theme", maxAge: 1h) }
type Session {
    token    string  @cookie("sid", path: "/", domain: ".example.com", maxAge: 24h, httpOnly: true, secure: true, sameSite: strict)
    remember cookie? @cookie("remember", maxAge: 2592000)
    plain    string  @cookie(sameSite: lax)
}
error Unauthorized Locked { until cookie @cookie("lock", httpOnly: false) }
service Auth {
    @errors(Locked)
    post Login /login { request LoginReq  response Session }
}`)
}

// An attribute @cookie does not take, one given twice, a value of the wrong kind and a value no
// browser keeps are refused, each where it is written.
func TestCookieAttributesAreChecked(t *testing.T) {
	for src, want := range map[string]struct{ code, msg string }{
		`color: red`:                    {CodeDecoratorArgType, `@cookie takes no argument "color"`},
		`maxAge: 1h, maxAge: 2h`:        {CodeDecoratorArgType, `argument "maxAge" is given twice`},
		`httpOnly: "yes"`:               {CodeDecoratorArgType, "httpOnly: expected bool, got string"},
		`path: "api"`:                   {CodeDecoratorArgValue, `path "api" must start with "/"`},
		`path: "/a;b"`:                  {CodeDecoratorArgValue, `path "/a;b" must start with "/" and hold no ";"`},
		`domain: "bad domain"`:          {CodeDecoratorArgValue, `domain "bad domain" is no host name`},
		`maxAge: 500ms`:                 {CodeDecoratorArgValue, "maxAge 500ms is no whole number of seconds above zero"},
		`sameSite: loose`:               {CodeDecoratorArgValue, "sameSite loose is not lax, strict or none"},
		`sameSite: none, secure: false`: {CodeDecoratorConflict, "sameSite none needs secure"},
	} {
		d := expectError(t, `type R { c string @cookie("c", `+src+`) }
service S { get A /a { response R } }`, want.code)
		expectMessage(t, d, want.msg)
	}
}

// Named arguments stay refused on every decorator but @cookie.
func TestNamedArgumentsStayRefusedElsewhere(t *testing.T) {
	d := expectError(t, `type R { c string @header("c", name: "x") }`, CodeDecoratorArgType)
	expectMessage(t, d, `named argument "name" is not supported`)
}

// A cookie's name is its positional argument alone: a named attribute is never read as it.
func TestCookieAttributesDoNotNameTheCookie(t *testing.T) {
	pkg := mustClean(t, `type R { session string @cookie(path: "/x") }
service S { get A /a { response R } }`)
	if name := wire.WireName(ast.Fields(pkg.Types["R"].Body)[0], wire.BindCookie); name != "session" {
		t.Errorf("cookie name %q, want the field's, session", name)
	}
}

// A `cookie` sets a response cookie, one per @cookie field of a response or an error body: one
// without @cookie, in a map or a generic argument, behind a scalar, carrying a validator or a
// @default, and one a request holds are refused.
func TestCookieTypePositions(t *testing.T) {
	for name, c := range map[string]struct{ src, code, msg string }{
		"no @cookie": {`type R { c cookie }
service S { get A /a { response R } }`, CodeCookiePosition, "is a `cookie`, which only sets a response cookie"},
		"map value": {`type R { c map<string, cookie> }
service S { get A /a { response R } }`, CodeCookiePosition, "holds `cookie` in `map<string, cookie>`"},
		"generic argument": {`type Box<T> { v T }
type R { c Box<cookie> }
service S { get A /a { response R } }`, CodeCookiePosition, "holds `cookie` in `Box<cookie>`"},
		"array": {`type R { c cookie[] @cookie("c") }
service S { get A /a { response R } }`, CodeBindingType, "@cookie cannot bind to an array"},
		"request": {`type Q { c cookie @cookie("c") }
service S { post A /a { request Q } }`, CodeCookiePosition, "a request reads a cookie's value alone"},
		"scalar": {`scalar Sess cookie`, CodeScalarBadPrimitive, "cannot wrap cookie"},
		"validator": {`type R { c cookie @cookie("c") @length(1, 5) }
service S { get A /a { response R } }`, CodeDecoratorTypeMismatch, "is cookie"},
		"default": {`type R { c cookie? @cookie("c") @default("x") }
service S { get A /a { response R } }`, "", "a cookie has no literal default form"},
	} {
		t.Run(name, func(t *testing.T) {
			if c.code != "" {
				expectMessage(t, expectError(t, c.src, c.code), c.msg)
				return
			}
			_, diags := Analyze(parseFiles(t, c.src))
			for _, d := range diags {
				if strings.Contains(d.Msg, c.msg) {
					return
				}
			}
			t.Errorf("no diagnostic says %q: %v", c.msg, diags)
		})
	}
}

// A `cookie` field nested below a response binds nothing, as any binding there.
func TestNestedCookieFieldBindsNothing(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"api.craftgo": `package api
type Inner { c cookie @cookie("c") }
type R { inner Inner }
service S { get A /a { response R } }`,
	})
	if _, diags := AnalyzeProject(files, Options{DesignRoot: root}); findCode(diags, CodeBindingNested) == nil {
		t.Errorf("a nested cookie field passed: %v", codes(diags))
	}
}

// The constructor a `cookie` field gains is a Go name of its package like any other.
func TestCookieConstructorNameCollides(t *testing.T) {
	d := expectError(t, `type NewRTokenCookie { a string }
type R { token cookie @cookie("t") }
service S { get A /a { response R } }`, CodeDeclGoNameCollision)
	expectMessage(t, d, "NewRTokenCookie", "its cookie constructor")
}
