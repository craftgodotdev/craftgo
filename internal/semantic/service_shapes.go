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
			// Methods collide on verb plus resolved route shape, parameter
			// names erased; a pathless method routes by its kebab-cased name.
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
