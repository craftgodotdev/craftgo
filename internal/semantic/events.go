package semantic

import (
	"maps"
	"slices"
	"sort"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// decoratorContract is the decorator that overrides an event's contract name.
const decoratorContract = "contract"

// ResolvedEvent is one event contract, resolved against the project.
type ResolvedEvent struct {
	Decl *ast.EventDecl
	// Name is the DSL identifier.
	Name string
	// Contract is the identity on the wire: `<package>.<Name>`, or the
	// `@contract` argument when one is given.
	Contract string
	// PayloadPkg is the package declaring the payload type, which may differ
	// from the event's (`payload shared.Envelope`); "" when it does not resolve.
	PayloadPkg string
	// PayloadRef is the payload reference as written, generic arguments included.
	PayloadRef *ast.NamedTypeRef
	// PayloadArray reports a `payload T[]` contract; the other payload
	// fields then describe the element.
	PayloadArray bool
	// Payload is the payload's declaration; nil when it does not resolve.
	Payload *ast.TypeDecl
	// Doc is the documentation a target renders above the contract: the
	// `@doc("...")` argument when one is given, otherwise the leading
	// comment block.
	Doc []string
}

// Events returns every event declared in the project, ordered by contract
// name.
func (p *Project) events() []ResolvedEvent {
	if p == nil {
		return nil
	}
	var out []ResolvedEvent
	for _, pkgName := range slices.Sorted(maps.Keys(p.Packages)) {
		pkg := p.Packages[pkgName]
		if pkg == nil {
			continue
		}
		for _, name := range slices.Sorted(maps.Keys(pkg.Events)) {
			out = append(out, p.resolveEvent(pkg, pkg.Events[name]))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Contract < out[j].Contract })
	return out
}

// LookupEvent resolves an event reference written inside homePkg: a bare
// `OrderPlaced` against homePkg, a qualified `orders.OrderPlaced` against
// the named package.
func (p *Project) LookupEvent(homePkg, ref string) (ResolvedEvent, bool) {
	if p == nil || ref == "" {
		return ResolvedEvent{}, false
	}
	pkg, name := p.resolveName(homePkg, ref)
	if pkg == nil {
		return ResolvedEvent{}, false
	}
	d, ok := pkg.Events[name]
	if !ok {
		return ResolvedEvent{}, false
	}
	return p.resolveEvent(pkg, d), true
}

// resolveEvent resolves d, declared in pkg.
func (p *Project) resolveEvent(pkg *Package, d *ast.EventDecl) ResolvedEvent {
	re := ResolvedEvent{
		Decl:     d,
		Name:     d.Name,
		Contract: contractName(pkg.Name, d),
		Doc:      descriptionLines(d.Decorators, d.Doc),
	}
	if d.Payload == nil || d.Payload.Type == nil || d.Payload.Type.Name == nil {
		return re
	}
	re.PayloadRef = d.Payload.Type
	re.PayloadArray = d.Payload.Array
	if home, name := p.resolve(pkg.Name, d.Payload.Type.Name); home != nil {
		re.PayloadPkg = home.Name
		re.Payload = home.Types[name]
	}
	return re
}

// contractName returns the wire identity of an event declared in pkgName:
// the `@contract` argument when present, otherwise `<pkgName>.<Name>`.
func contractName(pkgName string, d *ast.EventDecl) string {
	if d == nil {
		return ""
	}
	if s, ok := ast.StringArg(d.Decorators, decoratorContract); ok {
		return s
	}
	if pkgName == "" {
		return d.Name
	}
	return pkgName + "." + d.Name
}
