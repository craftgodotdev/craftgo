package wire

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// The attributes `@cookie` names after the cookie's name, for the cookie the server sets.
const (
	CookiePath     = "path"
	CookieDomain   = "domain"
	CookieMaxAge   = "maxAge"
	CookieHTTPOnly = "httpOnly"
	CookieSecure   = "secure"
	CookieSameSite = "sameSite"
)

// The values of the sameSite attribute.
const (
	SameSiteLax    = "lax"
	SameSiteStrict = "strict"
	SameSiteNone   = "none"
)

// CookieAttrs are the attributes of a cookie the server sets for a response's or an error's
// `@cookie` field.
type CookieAttrs struct {
	Path, Domain string
	// MaxAge is the cookie's lifetime in seconds; 0 sends none, and the cookie lasts the session.
	MaxAge           int64
	HTTPOnly, Secure bool
	// SameSite is [SameSiteLax], [SameSiteStrict] or [SameSiteNone].
	SameSite string
}

// DefaultCookieAttrs are the attributes of a cookie whose `@cookie` names none: sent to every
// path, hidden from scripts, over HTTPS only and kept from cross-site requests but top-level
// navigations.
var DefaultCookieAttrs = CookieAttrs{Path: "/", HTTPOnly: true, Secure: true, SameSite: SameSiteLax}

// CookieAttrsOf returns the attributes f's `@cookie` declares, each one it leaves out, or gives a
// value of the wrong kind, read as [DefaultCookieAttrs] has it.
func CookieAttrsOf(f *ast.Field) CookieAttrs {
	attrs := DefaultCookieAttrs
	d := ast.FindDecorator(f.Decorators, BindingCookie)
	if d == nil {
		return attrs
	}
	for _, a := range d.Args {
		if a == nil || !a.Named {
			continue
		}
		switch v := a.Value.(type) {
		case *ast.StringLit:
			switch a.Name {
			case CookiePath:
				attrs.Path = v.Value
			case CookieDomain:
				attrs.Domain = v.Value
			}
		case *ast.BoolLit:
			switch a.Name {
			case CookieHTTPOnly:
				attrs.HTTPOnly = v.Value
			case CookieSecure:
				attrs.Secure = v.Value
			}
		case *ast.DurationLit:
			if d, ok := lexer.ParseDuration(v.Text); ok && a.Name == CookieMaxAge {
				attrs.MaxAge = int64(d.Seconds())
			}
		case *ast.IntLit:
			if a.Name == CookieMaxAge {
				attrs.MaxAge = v.Value
			}
		case *ast.IdentExpr:
			if a.Name == CookieSameSite && v.Name != nil {
				attrs.SameSite = v.Name.String()
			}
		}
	}
	return attrs
}
