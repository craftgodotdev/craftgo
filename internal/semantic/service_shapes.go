// Service-method shape checks (uniqueness, route collisions) + PathString helper.
package semantic

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

func (a *analyzer) checkServiceMethods() {
	for _, si := range a.pkg.Services {
		seenName := map[string]lexer.Position{}
		seenRoute := map[string]lexer.Position{}
		for _, m := range si.Methods {
			if prev, ok := seenName[m.Name]; ok {
				d := a.diag(m.Pos, m.Pos, lexer.SeverityError, CodeServiceDuplicateMethod,
					"duplicate method %q", m.Name)
				d.Related = related(prev, "first declared here")
			} else {
				seenName[m.Name] = m.Pos
			}
			// Key the collision by the RESOLVED route shape: the full route
			// (prefix / group / basePath joined, with the kebab method-name
			// fallback applied for a pathless method) with param names stripped
			// to `{}`. This matches the cross-service check, so two pathless
			// methods of one verb — whose auto-routes differ (`/ping` vs
			// `/health`) — no longer collide on an empty path, while `/x/{id}`
			// and `/x/{id1}` still do.
			route := a.resolveMethodPath(si.Primary, m)
			key := m.Verb + " " + routeShape(route)
			if prev, ok := seenRoute[key]; ok {
				d := a.diag(m.Pos, m.Pos, lexer.SeverityError, CodeServiceDuplicateRoute,
					"duplicate route %q", m.Verb+" "+route)
				d.Related = related(prev, "first declared here")
			} else {
				seenRoute[key] = m.Pos
			}
		}
	}
}

// checkDecoratorDuplicates rejects two `@same` decorators in the same
// declaration scope. Decorators are identified by their bare name; arguments
// don't disambiguate (`@tags("a")` + `@tags("b")` is still a duplicate). The
// second occurrence is reported, pointing back at the first for context. We
// walk every scope that can carry decorators: the file header, top-level
// declarations, fields inside type / error bodies, enum values, service
// methods, and middleware-declaration sites.

func PathString(p *ast.Path) string {
	if p == nil {
		return ""
	}
	var sb strings.Builder
	for _, s := range p.Segments {
		sb.WriteByte('/')
		if s.Param {
			sb.WriteByte('{')
			sb.WriteString(s.Literal)
			sb.WriteByte('}')
		} else {
			sb.WriteString(s.Literal)
		}
	}
	return sb.String()
}

// PathShape is PathString with every {param} replaced by `{}`. Two
// routes that route to the same HTTP destination have the same shape
// even when their parameter names differ — e.g. `/u/{id}` and
// `/u/{userId}` both reduce to `/u/{}`. Collision-detection keys
// MUST use this rather than PathString, otherwise a parameter rename
// silently bypasses the duplicate guard and net/http's mux panics at
// boot when both routes try to register against the same pattern.
func PathShape(p *ast.Path) string {
	if p == nil {
		return ""
	}
	var sb strings.Builder
	for _, s := range p.Segments {
		sb.WriteByte('/')
		if s.Param {
			sb.WriteString("{}")
		} else {
			sb.WriteString(s.Literal)
		}
	}
	return sb.String()
}
