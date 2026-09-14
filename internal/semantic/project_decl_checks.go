// Project-level declaration checks: service / middleware name uniqueness
// across packages.
package semantic

import (
	"fmt"
	"sort"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// declSite is one declaration of a name and the package holding it.
type declSite struct {
	pkg string
	pos lexer.Position
}

// checkProjectServiceUniqueness fires when two packages declare a
// primary `service` of the same name. Codegen writes per-service
// scaffolds under `internal/{routes,handler,logic}/<service>/`, so a
// cross-package duplicate would silently overwrite one set of
// scaffolds with the other.
func (r *refResolver) checkProjectServiceUniqueness() {
	sites := map[string][]declSite{}
	for pkgName, pkg := range r.proj.Packages {
		for name, si := range pkg.Services {
			if si == nil || si.Primary == nil {
				continue
			}
			sites[name] = append(sites[name], declSite{pkg: pkgName, pos: si.Primary.Pos})
		}
	}
	r.reportCrossPackageDuplicates(sites, CodeServiceCollision,
		"service %q is declared in multiple packages - codegen output directories collide; rename one")
}

// checkProjectMiddlewareUniqueness fires whenever the same middleware
// name is declared in more than one package. Bare cross-package refs
// (`@middlewares(AuthRequired)`) resolve through the global union, so a
// collision would silently pick the first match the iterator hands back.
func (r *refResolver) checkProjectMiddlewareUniqueness() {
	sites := map[string][]declSite{}
	for pkgName, pkg := range r.proj.Packages {
		// Both tables feed one site set: a name is one middleware of one
		// kind, so `middleware X` here and `consume middleware X` there
		// collide the way two of a kind do.
		for _, table := range []map[string]*ast.MiddlewareDecl{pkg.Middlewares, pkg.ConsumeMiddlewares} {
			for name, m := range table {
				if m == nil {
					continue
				}
				sites[name] = append(sites[name], declSite{pkg: pkgName, pos: m.Pos})
			}
		}
	}
	r.reportCrossPackageDuplicates(sites, CodeMiddlewareCollision,
		"middleware %q is declared in multiple packages - names are global; rename or qualify references")
}

// reportCrossPackageDuplicates emits a diagnostic at every site of a name
// declared in more than one package, each carrying related entries for the
// others. Map iteration feeds sites, so the names and the sites within one
// name are both ordered: the diagnostic set must not shuffle between runs.
func (r *refResolver) reportCrossPackageDuplicates(sites map[string][]declSite, code, msg string) {
	names := make([]string, 0, len(sites))
	for name := range sites {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
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
			r.diags = append(r.diags, diag)
		}
	}
}
