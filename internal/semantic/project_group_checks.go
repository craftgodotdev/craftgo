package semantic

import (
	"fmt"
	"maps"
	"slices"
	"sort"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/route"
)

// segMember is a method a service contributes to an output directory; it
// scaffolds `<name>.go` there.
type segMember struct {
	name string
	kind string
	pos  lexer.Position
}

// segClaim is what one service puts into one output directory; group is ""
// for the service's own directory.
type segClaim struct {
	pkg     string
	svc     string
	group   string
	pos     lexer.Position
	members []segMember
}

// checkProjectGroupChecks rejects an output directory shared by services of
// different DSL packages, or by two services declaring one method name.
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
	for _, seg := range slices.Sorted(maps.Keys(claims)) {
		occs := claims[seg]
		// occs comes from map iteration; sort it for stable output.
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
		r.reportGroupMemberCollisions(seg, occs)
	}
}

// reportGroupPackageStraddle reports every service sharing seg, relating the
// others, when they come from more than one DSL package.
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

// reportGroupMemberCollisions reports each method whose name is declared by
// more than one service sharing seg.
func (r *refResolver) reportGroupMemberCollisions(seg string, occs []segClaim) {
	type owner struct {
		claim  segClaim
		member segMember
	}
	byName := map[string][]owner{}
	for _, o := range occs {
		for _, m := range o.members {
			byName[m.name] = append(byName[m.name], owner{claim: o, member: m})
		}
	}
	for _, name := range slices.Sorted(maps.Keys(byName)) {
		owners := byName[name]
		if len(owners) < 2 {
			continue
		}
		for i, o := range owners {
			diag := Diagnostic{
				Pos:      o.member.pos,
				End:      o.member.pos,
				Severity: lexer.SeverityError,
				Code:     CodeGroupMethodCollision,
				Msg: fmt.Sprintf(
					"%s %q of service %q collides with another service's %s of the same name in shared output directory %q - stubs are one file per member and the generated function is named after it, so both would claim %s.go and declare the same function; rename one or split the group",
					o.member.kind, name, o.claim.svc, o.member.kind, seg, name),
			}
			for j, other := range owners {
				if j == i {
					continue
				}
				diag.Related = append(diag.Related, lexer.Related{
					Pos: other.member.pos,
					Msg: fmt.Sprintf("also declared by service %q, which emits here (%s)", other.claim.svc, claimSource(other.claim.group)),
				})
			}
			r.diags = append(r.diags, diag)
		}
	}
}

// blockStubMembers lists the members of one service block that scaffold a
// file into the service logic folder, in source order.
func blockStubMembers(block *ast.ServiceDecl) []segMember {
	if block == nil {
		return nil
	}
	var out []segMember
	for _, m := range block.Methods() {
		out = append(out, segMember{name: m.Name, kind: "method", pos: m.Pos})
	}
	return out
}

// claimSource says, for diagnostics, how a service came to emit into a
// directory: its `@group`, or its own name.
func claimSource(group string) string {
	if group == "" {
		return "its own service directory, no @group"
	}
	return fmt.Sprintf("@group(%q)", group)
}

// serviceSegmentClaims returns, by segment, each output directory a
// service's blocks put methods into; blocks sharing one merge into a claim.
func serviceSegmentClaims(pkgName, svcName string, si *ServiceInfo, fileCase string) map[string]segClaim {
	out := map[string]segClaim{}
	if si == nil {
		return out
	}
	primaryGroup := route.ServiceGroup(si.Primary)
	add := func(block *ast.ServiceDecl, group string) {
		members := blockStubMembers(block)
		if len(members) == 0 {
			return
		}
		seg := route.OutputSegment(svcName, group, fileCase)
		claim, seen := out[seg]
		if !seen {
			claim = segClaim{pkg: pkgName, svc: svcName, group: group, pos: groupAnchor(block)}
		}
		claim.members = append(claim.members, members...)
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

// resolvedFileCase returns fileCase, or [config.DefaultFileCase] when unset.
func resolvedFileCase(fileCase string) string {
	if fileCase == "" {
		return config.DefaultFileCase
	}
	return fileCase
}
