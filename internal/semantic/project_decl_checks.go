// Project-level declaration checks: cross-package extend orphans, service /
// middleware name uniqueness, and @middlewares / @errors reference
// resolution against the full project.
package semantic

import (
	"fmt"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// checkProjectExtendOrphans walks every package's orphan
// `extend service` decls (those with no primary in the same
// package) and fires a tailored diagnostic. Two outcomes:
//
//   - Primary lives in a SIBLING package → the message names that
//     package and explains the per-package extend rule. The fix is
//     unambiguous (declare the extend inside the owning package).
//   - Primary doesn't exist anywhere → the "no primary declaration"
//     message under the same [CodeServiceExtendOrphan] code.
//
// The per-package pass is muted under [Options.skipExtendOrphanCheck]
// when [AnalyzeProject] runs, so this is the single emit site in
// project mode.
func (r *refResolver) checkProjectExtendOrphans() {
	primaryPkg := map[string]string{}
	primaryPos := map[string]lexer.Position{}
	for pkgName, pkg := range r.proj.Packages {
		for name, si := range pkg.Services {
			if si == nil || si.Primary == nil {
				continue
			}
			primaryPkg[name] = pkgName
			primaryPos[name] = si.Primary.Pos
		}
	}
	for _, pkg := range r.proj.Packages {
		for name, si := range pkg.Services {
			if si == nil || si.Primary != nil {
				continue
			}
			otherPkg, found := primaryPkg[name]
			for _, e := range si.Extends {
				diag := Diagnostic{
					Pos:      e.Pos,
					End:      e.Pos,
					Severity: lexer.SeverityError,
					Code:     CodeServiceExtendOrphan,
				}
				if found {
					diag.Msg = fmt.Sprintf(
						"extend service %q: primary lives in package %q - extend declarations are per-package, move this block into that package or rename the service",
						name, otherPkg)
					diag.Related = []lexer.Related{{
						Pos: primaryPos[name],
						Msg: "primary service declared here",
					}}
				} else {
					diag.Msg = fmt.Sprintf("extend service %q has no primary declaration", name)
				}
				r.diags = append(r.diags, diag)
			}
		}
	}
}

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
