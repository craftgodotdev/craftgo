// Event and consumer rules: payload shape, contract-name uniqueness, and
// consumer reference resolution.
package semantic

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/route"
)

// checkEvents runs the per-package event and consumer rules. Contract
// uniqueness and cross-package reference resolution run at project level
// (see [refResolver.checkProjectEvents]).
func (a *analyzer) checkEvents() {
	for _, name := range sortedNames(a.pkg.Events) {
		a.checkEvent(a.pkg.Events[name].Decl)
	}
	a.checkConsumerShapes()
	a.checkConsumerStubCollisions()
}

// checkConsumerStubCollisions rejects a consumer sharing a name with a
// method of the same service: both scaffold `<name>.go` into the service
// logic folder.
func (a *analyzer) checkConsumerStubCollisions() {
	for _, svcName := range sortedNames(a.pkg.Services) {
		si := a.pkg.Services[svcName]
		if si == nil {
			continue
		}
		methods := map[string]lexer.Position{}
		for _, m := range si.Methods {
			methods[m.Name] = m.Pos
		}
		for _, c := range si.Consumers {
			prev, clash := methods[c.Name]
			if !clash {
				continue
			}
			d := a.diag(c.Pos, c.Pos, lexer.SeverityError, CodeConsumerCollision,
				"consumer %q of service %q shares its name with a method - both scaffold %s.go in the service logic folder; rename one",
				c.Name, svcName, idents.FileName(c.Name, resolvedFileCase(a.opts.FileCase)))
			d.Related = related(prev, "method declared here")
		}
	}
}

// checkEvent validates one event's payload clause and `@contract`
// argument.
func (a *analyzer) checkEvent(d *ast.EventDecl) {
	if d.Payload == nil || d.Payload.Type == nil || d.Payload.Type.Name == nil {
		a.diag(d.Pos, d.Pos, lexer.SeverityError, CodeEventPayloadMissing,
			"event %q has no payload - add `payload <Type>` naming the type the contract carries", d.Name)
		return
	}
	a.checkContractArg(d)
	a.checkPayloadKind(d)
}

// checkContractArg rejects an `@contract` value that is empty or carries
// whitespace.
func (a *analyzer) checkContractArg(d *ast.EventDecl) {
	for _, dec := range d.Decorators {
		if dec == nil || dec.Name != DecoratorContract {
			continue
		}
		name, ok := DecoratorStringArg([]*ast.Decorator{dec}, DecoratorContract)
		if !ok {
			continue
		}
		if name == "" || strings.ContainsAny(name, " \t\n") {
			a.diag(dec.Pos, decoratorEnd(dec), lexer.SeverityError, CodeEventContractFormat,
				"@contract on event %q must be a non-empty name without whitespace", d.Name)
		}
	}
}

// checkPayloadKind reports an event payload that resolves to something
// other than a struct type. Cross-package refs whose package is unknown,
// and names nothing declares, are left to the reference pass.
func (a *analyzer) checkPayloadKind(d *ast.EventDecl) {
	ref := d.Payload.Type.Name.String()
	pkgName, name := splitQualified(ref, a.pkg.Name)
	home := a.pkg
	if pkgName != a.pkg.Name {
		if a.proj == nil {
			return
		}
		home = a.proj.Packages[pkgName]
	}
	if home == nil {
		return
	}
	if _, ok := home.Types[name]; ok {
		return
	}
	if _, isOther := lookupNonType(home, name); isOther {
		a.diag(d.Payload.Pos, d.Payload.Pos, lexer.SeverityError, CodeEventPayloadKind,
			"event %q payload %q is not a struct type - a payload must name a `type` declaration so the contract has named fields", d.Name, ref)
	}
}

// lookupNonType reports whether name resolves in pkg to a declaration
// that is not a struct type.
func lookupNonType(pkg *Package, name string) (ast.Decl, bool) {
	if d, ok := pkg.Enums[name]; ok {
		return d, true
	}
	if d, ok := pkg.Scalars[name]; ok {
		return d, true
	}
	if d, ok := pkg.Errors[name]; ok {
		return d, true
	}
	return nil, false
}

// checkConsumerShapes rejects a consumer with no `event` clause and a
// service that consumes the same contract twice.
func (a *analyzer) checkConsumerShapes() {
	for _, svcName := range sortedNames(a.pkg.Services) {
		si := a.pkg.Services[svcName]
		if si == nil {
			continue
		}
		seen := map[string]lexer.Position{}
		for _, c := range si.Consumers {
			ref := eventRefName(c.Event)
			if ref == "" {
				a.diag(c.Pos, c.Pos, lexer.SeverityError, CodeConsumerEventMissing,
					"consumer %q has no event - add `event <Name>` naming the contract it handles", c.Name)
				continue
			}
			if prev, dup := seen[ref]; dup {
				d := a.diag(c.Pos, c.Pos, lexer.SeverityError, CodeConsumerDuplicate,
					"service %q already consumes %q - one consumer per contract per service", svcName, ref)
				d.Related = related(prev, "first consumed here")
				continue
			}
			seen[ref] = c.Pos
		}
	}
}

// checkConsumerHandlerCollisions rejects two consuming services whose
// generated handler file would carry one name. The handler set is one file
// per service at the root of the transport output
// ([route.ConsumersFileName]), and the file case folds names that differ
// only in capitalisation, so the second write would replace the first and
// one service's consumers would stop being subscribed. Every claimant is
// flagged, each pointing at the others.
func (r *refResolver) checkConsumerHandlerCollisions() {
	type owner struct {
		svc string
		pkg string
		pos lexer.Position
	}
	byFile := map[string][]owner{}
	var order []string
	for _, pkgName := range sortedNames(r.proj.Packages) {
		pkg := r.proj.Packages[pkgName]
		if pkg == nil {
			continue
		}
		for _, svcName := range sortedNames(pkg.Services) {
			si := pkg.Services[svcName]
			if si == nil || si.Primary == nil || len(si.Consumers) == 0 {
				continue
			}
			file := route.ConsumersFileName(svcName, r.fileCase) + ".go"
			if _, seen := byFile[file]; !seen {
				order = append(order, file)
			}
			byFile[file] = append(byFile[file], owner{svc: svcName, pkg: pkgName, pos: si.Primary.Pos})
		}
	}
	for _, file := range order {
		owners := byFile[file]
		if len(owners) < 2 {
			continue
		}
		for i, o := range owners {
			d := r.diag(o.pos, lexer.SeverityError, CodeConsumerHandlerCollision,
				"service %q consumes events, and its handler set claims %s at the root of the transport output, which another service also claims - the file holds one service's handlers, so the second write would drop these consumers; rename one service",
				o.svc, file)
			for j, other := range owners {
				if j == i {
					continue
				}
				d.Related = append(d.Related, lexer.Related{
					Pos: other.pos,
					Msg: "service " + other.svc + " also claims it" + packageNote(other.pkg, o.pkg),
				})
			}
		}
	}
}

// checkProjectEvents rejects two events that would share one contract
// name, then applies the consumer-group rules.
func (r *refResolver) checkProjectEvents() {
	byContract := map[string]lexer.Position{}
	for _, ev := range r.proj.Events() {
		if ev.Contract == "" {
			continue
		}
		if prev, dup := byContract[ev.Contract]; dup {
			d := r.diag(ev.Decl.Pos, lexer.SeverityError, CodeEventContractCollision,
				"contract %q is declared twice - publisher and consumer cannot tell the two apart; rename one or set @contract", ev.Contract)
			d.Related = related(prev, "first declared here")
			continue
		}
		byContract[ev.Contract] = ev.Decl.Pos
	}
	r.checkConsumerGroups()
}

// groupClaim is one consumer's claim on a consumer group.
type groupClaim struct {
	pkg  string
	svc  string
	name string
	// contract the consumer reads, which decides whether two claims on
	// one group are two readers of one stream.
	contract string
	pos      lexer.Position
}

// service qualifies the service by package: two packages may each declare
// one of the same name, and they deploy apart.
func (c groupClaim) service() string { return c.pkg + "." + c.svc }

// label names the claim for a related-diagnostic line.
func (c groupClaim) label(from groupClaim) string {
	return "consumer " + c.svc + "." + c.name + packageNote(c.pkg, from.pkg)
}

// checkConsumerGroups resolves every consumer's event reference and
// applies the two rules a consumer group has to obey. Consumers of
// DIFFERENT contracts sharing one group inside one service is the point
// of the decorator and stays legal.
//
// Every claimant is flagged rather than whichever one came second: both
// sides are equally free to move, and the one that sorts later is not
// the one the author just edited.
func (r *refResolver) checkConsumerGroups() {
	claims, order := r.consumerGroupClaims()
	flagged := map[lexer.Position]bool{}
	for _, group := range order {
		for _, peers := range claimsByContract(claims[group]) {
			if len(peers) < 2 {
				continue
			}
			for _, c := range peers {
				flagged[c.pos] = true
				d := r.diag(c.pos, lexer.SeverityError, CodeConsumerGroupCollision,
					"consumer group %q reads contract %q more than once - the members of a group share a contract between them, so the two would split it instead of each receiving every message; give one of them its own @consumerGroup",
					group, c.contract)
				d.Related = relatedPeers(c, peers)
			}
		}
	}
	for _, group := range order {
		peers := claims[group]
		if !spansServices(peers) {
			continue
		}
		for _, c := range peers {
			if flagged[c.pos] {
				continue
			}
			d := r.diag(c.pos, lexer.SeverityError, CodeConsumerGroupCrossService,
				"consumer group %q is claimed by more than one service - a group requires every process that joins it to register the same consumers, and two services deploy as separate binaries: each would join handling only its own contracts and skip the rest, losing those messages; give each service its own @consumerGroup",
				group)
			d.Related = relatedPeers(c, peers)
		}
	}
}

// consumerGroupClaims resolves every consumer to its group, reporting an
// event reference that names no contract. The returned order lists the
// groups deterministically.
func (r *refResolver) consumerGroupClaims() (map[string][]groupClaim, []string) {
	claims := map[string][]groupClaim{}
	var order []string
	for _, pkgName := range sortedNames(r.proj.Packages) {
		pkg := r.proj.Packages[pkgName]
		if pkg == nil {
			continue
		}
		for _, name := range sortedNames(pkg.Consumers) {
			ci := pkg.Consumers[name]
			c := ci.Decl
			ref := eventRefName(c.Event)
			if ref == "" {
				continue
			}
			ev, ok := r.proj.LookupEvent(pkg.Name, ref)
			if !ok {
				r.diag(c.Event.Pos, lexer.SeverityError, CodeConsumerEventUnknown,
					"consumer %q references event %q, which no package declares", c.Name, ref)
				continue
			}
			group := ConsumerGroup(pkg.Name, ci.Service, pkg.Services[ci.Service], c)
			if _, seen := claims[group]; !seen {
				order = append(order, group)
			}
			claims[group] = append(claims[group], groupClaim{
				pkg: pkgName, svc: ci.Service, name: c.Name, contract: ev.Contract, pos: c.Pos,
			})
		}
	}
	return claims, order
}

// claimsByContract buckets one group's claims by the contract they read,
// in first-seen order.
func claimsByContract(claims []groupClaim) [][]groupClaim {
	index := map[string]int{}
	var out [][]groupClaim
	for _, c := range claims {
		i, seen := index[c.contract]
		if !seen {
			index[c.contract] = len(out)
			out = append(out, []groupClaim{c})
			continue
		}
		out[i] = append(out[i], c)
	}
	return out
}

// spansServices reports whether one group's claims come from more than
// one service.
func spansServices(claims []groupClaim) bool {
	if len(claims) == 0 {
		return false
	}
	for _, c := range claims[1:] {
		if c.service() != claims[0].service() {
			return true
		}
	}
	return false
}

// relatedPeers links a claim's diagnostic to every other claimant.
func relatedPeers(self groupClaim, peers []groupClaim) []lexer.Related {
	var out []lexer.Related
	for _, p := range peers {
		if p.pos == self.pos {
			continue
		}
		out = append(out, lexer.Related{Pos: p.pos, Msg: p.label(self) + " claims it too"})
	}
	return out
}
