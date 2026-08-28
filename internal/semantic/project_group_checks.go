// Project-level `@group` checks. Sharing a group between services is
// supported - that is what the decorator is for - so these passes police
// only the two things a shared output directory cannot absorb: a Go
// package declaration per directory, and one file per method name.
package semantic

import (
	"fmt"
	"sort"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/route"
)

// segMethod is one method a service contributes to an output directory.
type segMethod struct {
	name string
	pos  lexer.Position
}

// segClaim is everything one service puts into one output directory: the
// package it was declared in, the `@group` that took it there ("" = the
// service's own directory), the token to anchor a diagnostic at, and the
// methods it contributes. A service reaching one directory from several
// blocks produces a single claim carrying all their methods.
type segClaim struct {
	pkg     string
	svc     string
	group   string
	pos     lexer.Position
	methods []segMethod
}

// checkProjectGroupChecks runs both shared-directory passes over the
// project. `@group` REPLACES the service-name segment
// ([route.OutputSegment]), which is what lets several services share a
// folder: their handlers and stubs are per-method files that sit side by
// side, and codegen emits ONE routes.go per directory registering all of
// them. Two constraints survive that merge, and both are hard errors
// because neither has a sensible generated form:
//
//   - A directory is one Go package. Generated files take their `package`
//     declaration from the DSL package that declared the service, so a
//     folder fed by two DSL packages cannot compile.
//   - A method owns a filename and an exported handler name inside that
//     package, so two services in one folder cannot both declare it.
func (r *refResolver) checkProjectGroupChecks() {
	claims := map[string][]segClaim{}
	for pkgName, pkg := range r.proj.Packages {
		if pkg == nil {
			continue
		}
		for svcName, si := range pkg.Services {
			for seg, claim := range serviceSegmentClaims(pkgName, svcName, si, r.fileCase) {
				claims[seg] = append(claims[seg], claim)
			}
		}
	}
	segs := make([]string, 0, len(claims))
	for seg := range claims {
		segs = append(segs, seg)
	}
	sort.Strings(segs)
	for _, seg := range segs {
		occs := claims[seg]
		// Map iteration feeds occs, so order it before emitting: the
		// diagnostic set must not shuffle between runs.
		sort.Slice(occs, func(i, j int) bool {
			if occs[i].pkg != occs[j].pkg {
				return occs[i].pkg < occs[j].pkg
			}
			return occs[i].svc < occs[j].svc
		})
		if len(occs) < 2 {
			continue
		}
		r.reportGroupPackageStraddle(seg, occs)
		r.reportGroupMethodCollisions(seg, occs)
	}
}

// reportGroupPackageStraddle fires when the services sharing seg were not
// all declared in the same DSL package. Every claimant is flagged, each
// pointing at the others, so the editor underlines the whole conflict.
func (r *refResolver) reportGroupPackageStraddle(seg string, occs []segClaim) {
	first := occs[0].pkg
	straddles := false
	for _, o := range occs {
		if o.pkg != first {
			straddles = true
			break
		}
	}
	if !straddles {
		return
	}
	for i, o := range occs {
		diag := Diagnostic{
			Pos:      o.pos,
			End:      o.pos,
			Severity: lexer.SeverityError,
			Code:     CodeGroupPackageStraddle,
			Msg: fmt.Sprintf(
				"service %q (package %q) shares output directory %q with a service from another package - generated files take their Go package from the DSL package, so the directory would hold two different `package` declarations and fail to compile; sharing a @group is fine within one DSL package, otherwise give them separate groups",
				o.svc, o.pkg, seg),
		}
		for j, other := range occs {
			if j == i || other.pkg == o.pkg {
				continue
			}
			diag.Related = append(diag.Related, lexer.Related{
				Pos: other.pos,
				Msg: fmt.Sprintf("service %q in package %q also emits here (%s)", other.svc, other.pkg, claimSource(other.group)),
			})
		}
		r.diags = append(r.diags, diag)
	}
}

// reportGroupMethodCollisions fires when two services sharing seg declare
// the same method name. Anchored at the method, not the service, because
// renaming the method is the fix.
func (r *refResolver) reportGroupMethodCollisions(seg string, occs []segClaim) {
	type owner struct {
		claim  segClaim
		method segMethod
	}
	byName := map[string][]owner{}
	var order []string
	for _, o := range occs {
		for _, m := range o.methods {
			if _, seen := byName[m.name]; !seen {
				order = append(order, m.name)
			}
			byName[m.name] = append(byName[m.name], owner{claim: o, method: m})
		}
	}
	sort.Strings(order)
	for _, name := range order {
		owners := byName[name]
		if len(owners) < 2 {
			continue
		}
		for i, o := range owners {
			diag := Diagnostic{
				Pos:      o.method.pos,
				End:      o.method.pos,
				Severity: lexer.SeverityError,
				Code:     CodeGroupMethodCollision,
				Msg: fmt.Sprintf(
					"method %q of service %q collides with another service's method of the same name in shared output directory %q - handlers and stubs are one file per method and the handler function is named after it, so both would claim %s.go and declare the same function; rename one method or split the group",
					name, o.claim.svc, seg, name),
			}
			for j, other := range owners {
				if j == i {
					continue
				}
				diag.Related = append(diag.Related, lexer.Related{
					Pos: other.method.pos,
					Msg: fmt.Sprintf("also declared by service %q, which emits here (%s)", other.claim.svc, claimSource(other.claim.group)),
				})
			}
			r.diags = append(r.diags, diag)
		}
	}
}

// claimSource renders how a service came to emit into a directory, for the
// diagnostic text: through an explicit `@group`, or by being ungrouped and
// taking the directory named after itself.
func claimSource(group string) string {
	if group == "" {
		return "its own service directory, no @group"
	}
	return fmt.Sprintf("@group(%q)", group)
}

// serviceSegmentClaims returns every output directory one service emits
// into, keyed by segment, with the methods it puts in each. A service
// reaching one segment from several blocks - the primary plus an extend
// that repeats its @group - merges into a single claim, because those
// methods share a routes file rather than competing for one.
//
// A block contributes only the methods it declares: codegen derives its
// folder set from the merged method list, so a method-less block emits
// nothing. The claim is anchored at the `@group` decorator when the block
// carries one (the token an author edits to change the layout) and at the
// service declaration otherwise.
func serviceSegmentClaims(pkgName, svcName string, si *ServiceInfo, fileCase string) map[string]segClaim {
	out := map[string]segClaim{}
	if si == nil {
		return out
	}
	primaryGroup := route.ServiceGroup(si.Primary)
	add := func(block *ast.ServiceDecl, group string) {
		if block == nil || len(block.Methods()) == 0 {
			return
		}
		seg := route.OutputSegment(svcName, group, fileCase)
		claim, seen := out[seg]
		if !seen {
			claim = segClaim{pkg: pkgName, svc: svcName, group: group, pos: groupAnchor(block)}
		}
		for _, m := range block.Methods() {
			claim.methods = append(claim.methods, segMethod{name: m.Name, pos: m.Pos})
		}
		out[seg] = claim
	}
	add(si.Primary, primaryGroup)
	for _, e := range si.Extends {
		add(e, route.EffectiveGroup(e, primaryGroup))
	}
	return out
}

// groupAnchor returns the position a shared-directory diagnostic points at:
// the block's own `@group` decorator when it has one, else the block itself.
func groupAnchor(block *ast.ServiceDecl) lexer.Position {
	if block == nil {
		return lexer.Position{}
	}
	for _, d := range block.Decorators {
		if d != nil && d.Name == "group" {
			return d.Pos
		}
	}
	return block.Pos
}

// resolvedFileCase returns the `output.fileCase` the emitters will use.
// The manifest defaults an unset value to snake before codegen reads it;
// the analyser mirrors that so the ungrouped service directory it compares
// is the one that ends up on disk.
func resolvedFileCase(fileCase string) string {
	if fileCase == "" {
		return config.FileCaseSnake
	}
	return fileCase
}
