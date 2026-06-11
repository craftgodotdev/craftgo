// Project-level declaration checks: cross-package extend orphans, service /
// middleware name uniqueness, and @middlewares / @errors reference
// resolution against the full project.
package semantic

import (
	"fmt"
	"strings"

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

// checkProjectMiddlewareRefs validates `@middlewares(...)` arguments
// across the entire project. The per-package analyser skips this check
// (under [Options.skipMiddlewareRefCheck]) so a name declared in one
// package can be referenced from another. We accept a name when at
// least one package in the project declares a `middleware Name`; if
// no package does, we report [CodeDecoratorRef] at the reference.
//
// Cross-package middleware references stay UNQUALIFIED - the DSL has
// no syntax for `pkg.MiddlewareName` in decorator argument lists, and
// adding one would force a deeper change to the decorator parser.
// Name collisions across packages are rare enough in practice that
// the framework leans on convention (one canonical declaration per
// name) rather than a strict resolver.
func (r *refResolver) checkProjectMiddlewareRefs(files []*ast.File) {
	declared := map[string]bool{}
	for _, pkg := range r.proj.Packages {
		for name := range pkg.Middlewares {
			declared[name] = true
		}
	}
	for _, f := range files {
		for _, d := range f.Decls {
			s, ok := d.(*ast.ServiceDecl)
			if !ok {
				continue
			}
			r.checkMiddlewareDecorators(s.Decorators, declared)
			for _, m := range s.Methods() {
				r.checkMiddlewareDecorators(m.Decorators, declared)
			}
		}
	}
}

// checkMiddlewareDecorators inspects a decorator slice for any
// `@middlewares(...)` and emits a diagnostic for each argument whose
// value names an undeclared middleware. Two reference forms are
// accepted, in priority order:
//
//  1. Qualified `pkg.Name` - the prefix must match a package in the
//     project AND the trailing segment must be a `middleware Name`
//     declared in that package. This is the canonical form when
//     more than one package declares a middleware with the same
//     bare name (no ambiguity at the call site).
//  2. Bare `Name` - the trailing segment alone must be unique in
//     the union of every package's middleware table. Convenient
//     when names collide-free across packages.
//
// Cross-package lookup is intentional: the per-package analyser
// skips middleware-ref validation under
// [Options.skipMiddlewareRefCheck] so this resolver is the single
// authority on which references are valid.
func (r *refResolver) checkMiddlewareDecorators(decs []*ast.Decorator, declared map[string]bool) {
	for _, d := range decs {
		if d == nil || d.Name != "middlewares" {
			continue
		}
		for _, arg := range collectIdentOrStringArgs(d) {
			if r.middlewareRefResolves(arg.value, declared) {
				continue
			}
			r.diag(arg.pos, lexer.SeverityError, CodeDecoratorRef,
				"@middlewares: %q is not a declared middleware in any package", arg.value)
		}
	}
}

// middlewareRefResolves returns true when value is recognised as a
// valid middleware reference under either the qualified or bare form.
func (r *refResolver) middlewareRefResolves(value string, declared map[string]bool) bool {
	if dot := strings.LastIndexByte(value, '.'); dot >= 0 {
		pkgName := value[:dot]
		bare := value[dot+1:]
		pkg := r.proj.Packages[pkgName]
		if pkg == nil {
			return false
		}
		_, ok := pkg.Middlewares[bare]
		return ok
	}
	return declared[value]
}

// checkProjectErrorRefs validates every `@errors(...)` target against the
// project-wide error table. The per-package pass skips @errors along with
// the other cross-package decorator refs (middleware / security) because
// a target may be qualified (`shared.UnauthorizedErr`) and resolve in
// another package; without this project-level pass a typo like
// `@errors(NotFounds)` slips silently to codegen, which then emits no
// response for it. Mirrors [checkProjectMiddlewareRefs].
func (r *refResolver) checkProjectErrorRefs(files []*ast.File) {
	declared := map[string]bool{}
	for _, pkg := range r.proj.Packages {
		for name := range pkg.Errors {
			declared[name] = true
		}
	}
	for _, f := range files {
		for _, d := range f.Decls {
			s, ok := d.(*ast.ServiceDecl)
			if !ok {
				continue
			}
			r.checkErrorDecorators(s.Decorators, declared)
			for _, m := range s.Methods() {
				r.checkErrorDecorators(m.Decorators, declared)
			}
		}
	}
}

func (r *refResolver) checkErrorDecorators(decs []*ast.Decorator, declared map[string]bool) {
	for _, d := range decs {
		if d == nil || d.Name != "errors" {
			continue
		}
		for _, arg := range collectIdentOrStringArgs(d) {
			if r.errorRefResolves(arg.value, declared) {
				continue
			}
			r.diag(arg.pos, lexer.SeverityError, CodeDecoratorRef,
				"@errors: %q is not a declared error type in any package", arg.value)
		}
	}
}

// errorRefResolves mirrors [middlewareRefResolves] for error types: a
// qualified `pkg.Name` resolves against that package's error table, a
// bare name against the project-wide declared set.
func (r *refResolver) errorRefResolves(value string, declared map[string]bool) bool {
	if dot := strings.LastIndexByte(value, '.'); dot >= 0 {
		pkgName, bare := value[:dot], value[dot+1:]
		pkg := r.proj.Packages[pkgName]
		if pkg == nil {
			return false
		}
		_, ok := pkg.Errors[bare]
		return ok
	}
	return declared[value]
}
