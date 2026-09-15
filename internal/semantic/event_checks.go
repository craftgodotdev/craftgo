// Event rules: payload shape, `@contract` format, and contract-name
// uniqueness.
package semantic

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/prims"
)

// checkEvents runs the per-package event rules. Contract uniqueness runs
// at project level (see [refResolver.checkProjectEvents]).
func (a *analyzer) checkEvents() {
	for _, name := range sortedNames(a.pkg.Events) {
		a.checkEvent(a.pkg.Events[name])
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
// other than a struct type. An array payload (`payload T[]`) resolves
// its ELEMENT here, exactly as a scalar one resolves: an array of a
// declared type is a contract, an array of anything else is not.
// Cross-package refs whose package is unknown, and names nothing
// declares, are left to the reference pass.
func (a *analyzer) checkPayloadKind(d *ast.EventDecl) {
	ref := d.Payload.Type.Name.String()
	if prims.Is(ref) {
		// A primitive names no declaration, so neither the lookup below
		// nor the reference pass (which knows the primitive) would say
		// anything - and the contract would carry an unnamed, unvalidated
		// body.
		a.payloadKindDiag(d, ref)
		return
	}
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
		a.payloadKindDiag(d, ref)
	}
}

// payloadKindDiag reports a payload that is not a struct type.
func (a *analyzer) payloadKindDiag(d *ast.EventDecl, ref string) {
	a.diag(d.Payload.Pos, d.Payload.Pos, lexer.SeverityError, CodeEventPayloadKind,
		"event %q payload %q is not a struct type - a payload must name a `type` declaration so the contract has named fields", d.Name, ref)
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

// checkProjectEvents rejects two events that would share one contract
// name.
func (r *refResolver) checkProjectEvents() {
	byContract := map[string]lexer.Position{}
	for _, ev := range r.proj.Events() {
		if ev.Contract == "" {
			continue
		}
		if prev, dup := byContract[ev.Contract]; dup {
			d := r.diag(ev.Decl.Pos, lexer.SeverityError, CodeEventContractCollision,
				"contract %q is declared twice - a listener cannot tell the two apart; rename one or set @contract", ev.Contract)
			d.Related = related(prev, "first declared here")
			continue
		}
		byContract[ev.Contract] = ev.Decl.Pos
	}
}
