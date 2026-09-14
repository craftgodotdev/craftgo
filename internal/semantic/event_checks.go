// Event and consumer rules: payload shape, contract-name uniqueness, and
// consumer reference resolution.
package semantic

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// checkEvents runs the per-package event and consumer rules. Contract
// uniqueness and cross-package reference resolution run at project level
// (see [refResolver.checkProjectEvents]).
func (a *analyzer) checkEvents() {
	for _, name := range sortedNames(a.pkg.Events) {
		a.checkEvent(a.pkg.Events[name].Decl)
	}
	a.checkConsumerShapes()
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

// checkConsumerShapes rejects a consumer with no `event` clause.
func (a *analyzer) checkConsumerShapes() {
	for _, svcName := range sortedNames(a.pkg.Services) {
		si := a.pkg.Services[svcName]
		if si == nil {
			continue
		}
		for _, c := range si.Consumers {
			if eventRefName(c.Event) == "" {
				a.diag(c.Pos, c.Pos, lexer.SeverityError, CodeConsumerEventMissing,
					"consumer %q has no event - add `event <Name>` naming the contract it handles", c.Name)
			}
		}
	}
}

// checkProjectEvents rejects two events that would share one contract
// name, then resolves every consumer's event reference.
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
	r.checkConsumerEvents()
}

// checkConsumerEvents reports a consumer whose `event` clause names a
// contract no package declares.
func (r *refResolver) checkConsumerEvents() {
	for _, pkgName := range sortedNames(r.proj.Packages) {
		pkg := r.proj.Packages[pkgName]
		if pkg == nil {
			continue
		}
		for _, name := range sortedNames(pkg.Consumers) {
			c := pkg.Consumers[name].Decl
			ref := eventRefName(c.Event)
			if ref == "" {
				continue
			}
			if _, ok := r.proj.LookupEvent(pkg.Name, ref); !ok {
				r.diag(c.Event.Pos, lexer.SeverityError, CodeConsumerEventUnknown,
					"consumer %q references event %q, which no package declares", c.Name, ref)
			}
		}
	}
}
