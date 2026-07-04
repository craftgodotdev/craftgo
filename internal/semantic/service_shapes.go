// Service-method shape checks (uniqueness, route collisions).
package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/route"
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
			// methods of one verb - whose auto-routes differ (`/ping` vs
			// `/health`) - no longer collide on an empty path, while `/x/{id}`
			// and `/x/{id1}` still do.
			rt := a.resolveMethodPath(si.Primary, m)
			key := m.Verb + " " + route.Shape(rt)
			if prev, ok := seenRoute[key]; ok {
				d := a.diag(m.Pos, m.Pos, lexer.SeverityError, CodeServiceDuplicateRoute,
					"duplicate route %q", m.Verb+" "+rt)
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
