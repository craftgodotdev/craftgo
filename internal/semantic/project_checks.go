package semantic

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/route"
)

// declSite is one declaration of a name and the package holding it.
type declSite struct {
	pkg string
	pos lexer.Position
}

// checkProjectMiddlewareUniqueness rejects a middleware name declared in
// more than one package, as a bare reference resolves project-wide, and
// middlewares of distinct names whose scaffolds write one file.
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
	byFile := map[string][]string{}
	for name := range sites {
		file := idents.MiddlewareFileName(name, c.fileCase) + ".go"
		byFile[file] = append(byFile[file], name)
	}
	for _, file := range slices.Sorted(maps.Keys(byFile)) {
		names := byFile[file]
		if len(names) < 2 {
			continue
		}
		slices.Sort(names)
		var reports []siteReport
		for _, name := range names {
			for _, s := range sites[name] {
				reports = append(reports, siteReport{
					pos:  s.pos,
					msg:  fmt.Sprintf("middlewares %s all write the scaffold %s - rename all but one", quotedNames(names), file),
					note: fmt.Sprintf("middleware %q in package %q", name, s.pkg),
				})
			}
		}
		c.reportEverySite(CodeMiddlewareCollision, reports)
	}
}

// quotedNames renders names quoted and comma-separated.
func quotedNames(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = strconv.Quote(n)
	}
	return strings.Join(quoted, ", ")
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
		reports := make([]siteReport, len(occs))
		for i, o := range occs {
			reports[i] = siteReport{pos: o.pos, msg: fmt.Sprintf(msg, name), note: "also declared in package " + o.pkg}
		}
		c.reportEverySite(code, reports)
	}
}

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
func (c *projectChecks) checkProjectGroupChecks() {
	claims := map[string][]segClaim{}
	for pkgName, pkg := range c.proj.Packages {
		if pkg == nil {
			continue
		}
		for svcName, si := range pkg.Services {
			for seg, claim := range serviceSegmentClaims(pkgName, svcName, si, c.fileCase) {
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
		c.reportMethodNameClashes(seg, occs)
		if len(occs) < 2 {
			continue
		}
		c.reportGroupPackageStraddle(seg, occs)
		c.reportGroupMemberCollisions(seg, occs)
	}
}

// reportGroupPackageStraddle reports every service sharing seg, relating the
// others, when they come from more than one DSL package.
func (c *projectChecks) reportGroupPackageStraddle(seg string, occs []segClaim) {
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
	reports := make([]siteReport, len(occs))
	for i, o := range occs {
		reports[i] = siteReport{
			pos: o.pos,
			msg: fmt.Sprintf(
				"service %q (package %q) shares output directory %q with a service from another package - generated files take their Go package from the DSL package, so the directory would hold two different `package` declarations and fail to compile; sharing a @group is fine within one DSL package, otherwise give them separate groups",
				o.svc, o.pkg, seg),
			note: fmt.Sprintf("service %q in package %q also emits here (%s)", o.svc, o.pkg, claimSource(o.group)),
			peer: o.pkg,
		}
	}
	c.reportEverySite(CodeGroupPackageStraddle, reports)
}

// reportGroupMemberCollisions reports each method whose name is declared by
// more than one service sharing seg.
func (c *projectChecks) reportGroupMemberCollisions(seg string, occs []segClaim) {
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
		reports := make([]siteReport, len(owners))
		for i, o := range owners {
			reports[i] = siteReport{
				pos: o.member.pos,
				msg: fmt.Sprintf(
					"%s %q of service %q collides with another service's %s of the same name in shared output directory %q - stubs are one file per member and the generated function is named after it, so both would claim %s.go and declare the same function; rename one or split the group",
					o.member.kind, name, o.claim.svc, o.member.kind, seg, name),
				note: fmt.Sprintf("also declared by service %q, which emits here (%s)", o.claim.svc, claimSource(o.claim.group)),
			}
		}
		c.reportEverySite(CodeGroupMethodCollision, reports)
	}
}

// reportMethodNameClashes reports, among the methods scaffolded into seg,
// one Go package, a method named like the log.Logger each logic type embeds
// or writing a file the go command treats apart and, at every site, methods
// of distinct names that write one file, and each pair whose logic
// constructor and logic type share a name. A name declared twice is left to
// the duplicate-method and group rules.
func (c *projectChecks) reportMethodNameClashes(seg string, occs []segClaim) {
	type owner struct {
		svc    string
		member segMember
	}
	byName := map[string]owner{}
	byFile := map[string][]owner{}
	var firsts []owner
	for _, o := range occs {
		for _, m := range o.members {
			if _, dup := byName[m.name]; dup {
				continue
			}
			ow := owner{svc: o.svc, member: m}
			byName[m.name] = ow
			firsts = append(firsts, ow)
			base := idents.FileName(m.name, c.fileCase)
			if why := idents.GoFileProblem(base); why != "" {
				c.diag(m.pos, lexer.SeverityError, CodeMethodFileName,
					"method %q of service %q writes %s.go in output directory %q, but %s - rename the method", m.name, o.svc, base, seg, why)
			}
			byFile[base+".go"] = append(byFile[base+".go"], ow)
		}
	}
	report := func(owners []owner, msg string) {
		reports := make([]siteReport, len(owners))
		for i, o := range owners {
			reports[i] = siteReport{pos: o.member.pos, msg: msg,
				note: fmt.Sprintf("method %q of service %q", o.member.name, o.svc)}
		}
		c.reportEverySite(CodeMethodNameClash, reports)
	}
	for _, file := range slices.Sorted(maps.Keys(byFile)) {
		if owners := byFile[file]; len(owners) > 1 {
			names := make([]string, len(owners))
			for i, o := range owners {
				names[i] = o.member.name
			}
			report(owners, fmt.Sprintf("methods %s all write %s in output directory %q - rename all but one",
				quotedNames(names), file, seg))
		}
	}
	for _, o := range firsts {
		name := o.member.name
		if name == idents.LogicEmbed {
			c.diag(o.member.pos, lexer.SeverityError, CodeMethodNameClash,
				"method %q of service %q is named like the log.Logger its logic type %s embeds - rename it",
				name, o.svc, idents.LogicTypeName(name))
		}
		if rival, ok := byName[idents.LogicRival(name)]; ok {
			report([]owner{o, rival}, fmt.Sprintf("methods %q and %q both generate %s in output directory %q: the constructor of %s and the logic type of %s - rename one",
				name, rival.member.name, idents.LogicConstructorName(name), seg, idents.LogicTypeName(name), rival.member.name))
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
	for _, b := range si.blocks() {
		add(b, si.blockGroup(b))
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
