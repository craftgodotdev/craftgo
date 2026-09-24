package semantic

import (
	"fmt"
	"maps"
	"slices"
	"sort"

	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// declSite is one declaration of a name and the package holding it.
type declSite struct {
	pkg string
	pos lexer.Position
}

// checkProjectMiddlewareUniqueness rejects a middleware name declared in
// more than one package; a bare reference resolves project-wide.
func (c *projectChecks) checkProjectMiddlewareUniqueness() {
	sites := map[string][]declSite{}
	for pkgName, pkg := range c.proj.Packages {
		for name, m := range pkg.Middlewares {
			if m == nil {
				continue
			}
			sites[name] = append(sites[name], declSite{pkg: pkgName, pos: m.Pos})
		}
	}
	c.reportCrossPackageDuplicates(sites, CodeMiddlewareCollision,
		"middleware %q is declared in multiple packages - names are global; rename or qualify references")
}

// reportCrossPackageDuplicates reports every site of a name declared in more
// than one package, relating the others, in an order stable across runs.
func (c *projectChecks) reportCrossPackageDuplicates(sites map[string][]declSite, code, msg string) {
	for _, name := range slices.Sorted(maps.Keys(sites)) {
		occs := sites[name]
		if len(occs) < 2 {
			continue
		}
		sort.Slice(occs, func(i, j int) bool {
			if occs[i].pkg != occs[j].pkg {
				return occs[i].pkg < occs[j].pkg
			}
			if occs[i].pos.Filename != occs[j].pos.Filename {
				return occs[i].pos.Filename < occs[j].pos.Filename
			}
			return occs[i].pos.Offset < occs[j].pos.Offset
		})
		for i, o := range occs {
			diag := Diagnostic{
				Pos:      o.pos,
				End:      o.pos,
				Severity: lexer.SeverityError,
				Code:     code,
				Msg:      fmt.Sprintf(msg, name),
			}
			for j, other := range occs {
				if j == i {
					continue
				}
				diag.Related = append(diag.Related, lexer.Related{
					Pos: other.pos,
					Msg: "also declared in package " + other.pkg,
				})
			}
			c.diags = append(c.diags, diag)
		}
	}
}
