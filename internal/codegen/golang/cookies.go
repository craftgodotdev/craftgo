package golang

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// cookieLiteral renders the *http.Cookie f's `@cookie` sets, named name and holding value, with
// the attributes the field declares.
func cookieLiteral(f *ast.Field, name, value string) string {
	attrs := wire.CookieAttrsOf(f)
	parts := []string{"Name: " + strconv.Quote(name), "Value: " + value}
	if attrs.Path != "" {
		parts = append(parts, "Path: "+strconv.Quote(attrs.Path))
	}
	if attrs.Domain != "" {
		parts = append(parts, "Domain: "+strconv.Quote(attrs.Domain))
	}
	if attrs.MaxAge != 0 {
		parts = append(parts, "MaxAge: "+strconv.FormatInt(attrs.MaxAge, 10))
	}
	if attrs.HTTPOnly {
		parts = append(parts, "HttpOnly: true")
	}
	if attrs.Secure {
		parts = append(parts, "Secure: true")
	}
	if mode := sameSiteModes[attrs.SameSite]; mode != "" {
		parts = append(parts, "SameSite: "+mode)
	}
	return "&http.Cookie{" + strings.Join(parts, ", ") + "}"
}

// sameSiteModes spells each sameSite value as net/http's constant.
var sameSiteModes = map[string]string{
	wire.SameSiteLax:    "http.SameSiteLaxMode",
	wire.SameSiteStrict: "http.SameSiteStrictMode",
	wire.SameSiteNone:   "http.SameSiteNoneMode",
}

// isCookieField reports whether f is a `cookie` field, whose value the logic sets as a whole
// *http.Cookie.
func isCookieField(f *ast.Field) bool {
	return f.Type != nil && !f.Type.Array && f.Type.Named != nil && f.Type.Named.Name != nil && f.Type.Named.Name.String() == "cookie"
}

// renderCookieConstructors renders the constructor of each `cookie` field of body, which owner
// names in Go, adding net/http to imports when there is one.
func renderCookieConstructors(owner string, body []ast.TypeMember, imports *importSet) string {
	var out []string
	goNames := resolvedGoFieldNames(body)
	for i, f := range ast.Fields(body) {
		if !isCookieField(f) {
			continue
		}
		imports.use("net/http")
		name := wire.WireName(f, wire.BindCookie)
		fn := idents.CookieConstructorName(owner, goNames[i])
		out = append(out, fmt.Sprintf("// %s returns the cookie %s holding value, with the attributes %s declares.\nfunc %s(value string) *http.Cookie {\n\treturn %s\n}\n",
			fn, strconv.Quote(name), owner+"."+goNames[i], fn, cookieLiteral(f, name, "value")))
	}
	return strings.Join(out, "\n")
}

// renderCookieWrite renders the statement that hands the cookie rf sets to set, a function of
// one *http.Cookie expression: a `cookie` field's value under its declared name, when the logic
// set one, or a value field's string valueExpr with the declared attributes.
func renderCookieWrite(rf semantic.ResolvedField, field, valueExpr string, set func(string) string) string {
	name := wire.WireName(rf.Field, wire.BindCookie)
	if rf.Category == semantic.CatCookie {
		return fmt.Sprintf("if %s != nil {\n_c := *%s\n_c.Name = %s\n%s\n}", field, field, strconv.Quote(name), set("&_c"))
	}
	return set(cookieLiteral(rf.Field, name, valueExpr))
}
