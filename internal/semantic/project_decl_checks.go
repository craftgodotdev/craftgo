// Project-level declaration checks: service / middleware name uniqueness
// across packages.
package semantic

import (
	"fmt"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// checkProjectServiceUniqueness fires when two packages declare a
// primary `service` of the same name. Codegen writes per-service
// scaffolds under `internal/{routes,handler,logic}/<service>/`, so a
// cross-package duplicate would silently overwrite one set of
// scaffolds with the other. Diagnostics fire at every site with
// related entries pointing at the others.
func (r *refResolver) checkProjectServiceUniqueness() {
	type origin struct {
		pkg string
		pos lexer.Position
	}
	groups := map[string][]origin{}
	for pkgName, pkg := range r.proj.Packages {
		for name, si := range pkg.Services {
			if si == nil || si.Primary == nil {
				continue
			}
			groups[name] = append(groups[name], origin{pkg: pkgName, pos: si.Primary.Pos})
		}
	}
	for name, occs := range groups {
		if len(occs) < 2 {
			continue
		}
		for i, o := range occs {
			diag := Diagnostic{
				Pos:      o.pos,
				End:      o.pos,
				Severity: lexer.SeverityError,
				Code:     CodeServiceCollision,
				Msg: fmt.Sprintf("service %q is declared in multiple packages - codegen output directories collide; rename one",
					name),
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

// checkProjectMiddlewareUniqueness fires whenever the same middleware
// name is declared in more than one package. Bare cross-package refs
// (`@middlewares(AuthRequired)`) resolve through the global union, so
// a collision would silently pick the first match the iterator hands
// back - the diagnostic forces the author to rename or consolidate.
//
// Diagnostics are emitted at every conflicting declaration, with
// related entries pointing at the other occurrences, so the editor's
// "go to" actions land on each site.
func (r *refResolver) checkProjectMiddlewareUniqueness() {
	type origin struct {
		pkg  string
		decl *ast.MiddlewareDecl
	}
	groups := map[string][]origin{}
	for pkgName, pkg := range r.proj.Packages {
		for name, m := range pkg.Middlewares {
			groups[name] = append(groups[name], origin{pkg: pkgName, decl: m})
		}
	}
	for name, occs := range groups {
		if len(occs) < 2 {
			continue
		}
		for i, o := range occs {
			diag := Diagnostic{
				Pos:      o.decl.Pos,
				End:      o.decl.Pos,
				Severity: lexer.SeverityError,
				Code:     CodeMiddlewareCollision,
				Msg: fmt.Sprintf("middleware %q is declared in multiple packages - names are global; rename or qualify references",
					name),
			}
			for j, other := range occs {
				if j == i {
					continue
				}
				diag.Related = append(diag.Related, lexer.Related{
					Pos: other.decl.Pos,
					Msg: "also declared in package " + other.pkg,
				})
			}
			r.diags = append(r.diags, diag)
		}
	}
}
