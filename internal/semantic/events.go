// Event model: the language-independent view of the contracts a design
// declares. The resolved type carries every fact a target needs - the
// contract name, the payload's home package - already resolved.
package semantic

import (
	"sort"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// DecoratorContract overrides the derived contract name.
const DecoratorContract = "contract"

// ResolvedEvent is the layer-agnostic view of one event contract.
type ResolvedEvent struct {
	Decl    *ast.EventDecl
	Package string
	// Name is the DSL identifier.
	Name string
	// Contract is the identity on the wire: `<package>.<Name>`, or the
	// `@contract` argument when one is given. Transports address it
	// however they like; both sides agree on this string.
	Contract string
	// PayloadPkg and PayloadName name the payload type. PayloadPkg is
	// the package the type lives in, which is not always the event's own
	// package - a `payload shared.Envelope` resolves elsewhere.
	PayloadPkg  string
	PayloadName string
	// PayloadRef is the payload reference as written, including any
	// generic arguments. Targets render from this - PayloadName alone
	// drops the arguments, which a generic payload needs.
	PayloadRef *ast.NamedTypeRef
	// Payload is the resolved declaration, nil when the ref did not
	// resolve (the analyser has already reported that).
	Payload *ast.TypeDecl
	// Doc is the documentation a target renders above the contract: the
	// `@doc("...")` argument when one is given, otherwise the leading
	// comment block.
	Doc []string
}

// Events returns every event declared in the project, ordered by contract
// name.
func (p *Project) Events() []ResolvedEvent {
	if p == nil {
		return nil
	}
	var out []ResolvedEvent
	for _, pkgName := range sortedNames(p.Packages) {
		pkg := p.Packages[pkgName]
		if pkg == nil {
			continue
		}
		for _, name := range sortedNames(pkg.Events) {
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
	pkgName, name := splitQualified(ref, homePkg)
	pkg := p.Packages[pkgName]
	if pkg == nil {
		return ResolvedEvent{}, false
	}
	d, ok := pkg.Events[name]
	if !ok {
		return ResolvedEvent{}, false
	}
	return p.resolveEvent(pkg, d), true
}

// resolveEvent computes the layer-agnostic facts for one event.
func (p *Project) resolveEvent(pkg *Package, d *ast.EventDecl) ResolvedEvent {
	re := ResolvedEvent{
		Decl:     d,
		Package:  pkg.Name,
		Name:     d.Name,
		Contract: ContractName(pkg.Name, d),
		Doc:      descriptionLines(d.Decorators, d.Doc),
	}
	if d.Payload == nil || d.Payload.Type == nil || d.Payload.Type.Name == nil {
		return re
	}
	re.PayloadRef = d.Payload.Type
	re.PayloadPkg, re.PayloadName = splitQualified(d.Payload.Type.Name.String(), pkg.Name)
	if home := p.Packages[re.PayloadPkg]; home != nil {
		re.Payload = home.Types[re.PayloadName]
	}
	return re
}

// ContractName returns the wire identity of an event declared in pkgName:
// the `@contract` argument when present, otherwise `<pkgName>.<Name>`.
func ContractName(pkgName string, d *ast.EventDecl) string {
	if d == nil {
		return ""
	}
	if s, ok := DecoratorStringArg(d.Decorators, DecoratorContract); ok {
		return s
	}
	if pkgName == "" {
		return d.Name
	}
	return pkgName + "." + d.Name
}

// splitQualified splits `pkg.Name` into its parts, defaulting the
// qualifier to fallback for a bare name.
func splitQualified(ref, fallback string) (pkgName, name string) {
	for i := len(ref) - 1; i >= 0; i-- {
		if ref[i] == '.' {
			return ref[:i], ref[i+1:]
		}
	}
	return fallback, ref
}
