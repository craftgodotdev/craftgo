// Event model: the language-independent view of the contracts a service
// publishes and the consumers that handle them. The resolved types carry
// every fact a target needs - the contract name, the payload's home
// package - already resolved.
package semantic

import (
	"sort"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// Decorator names that shape an event contract.
const (
	// DecoratorContract overrides the derived contract name.
	DecoratorContract = "contract"
	// DecoratorConsumerGroup names the broker identity a consumer
	// joins. Valid on a consumer and on the service that declares it.
	DecoratorConsumerGroup = "consumerGroup"
)

// EventInfo is one declared event with the service that declares it.
type EventInfo struct {
	Decl    *ast.EventDecl
	Service string
}

// ConsumerInfo is one declared consumer with the service that declares it.
type ConsumerInfo struct {
	Decl    *ast.ConsumerDecl
	Service string
}

// ResolvedEvent is the layer-agnostic view of one event contract.
type ResolvedEvent struct {
	Decl    *ast.EventDecl
	Package string
	Service string
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
	Doc     []string
}

// PublishedBy returns the contracts svc publishes, in the order the
// service declares them. It is the one answer to "does this service
// publish anything" - reading it, rather than counting a service's event
// members, keeps every target agreeing with [ResolvedEvent.HasProducer]
// even if what makes a contract producerless ever changes.
func (p *Project) PublishedBy(pkg *Package, svc string) []ResolvedEvent {
	if p == nil || pkg == nil {
		return nil
	}
	info := pkg.Services[svc]
	if info == nil {
		return nil
	}
	out := make([]ResolvedEvent, 0, len(info.Events))
	for _, ed := range info.Events {
		ev := p.resolveEvent(pkg, &EventInfo{Decl: ed, Service: svc})
		if ev.HasProducer() {
			out = append(out, ev)
		}
	}
	return out
}

// HasProducer reports whether this design declares the service that
// publishes the contract. A contract declared at file level describes one
// published elsewhere - by another system, or by a service outside this
// design - so nothing here emits a publisher for it, lists it on the
// ServiceContext, or claims a `send` operation in the document.
func (e ResolvedEvent) HasProducer() bool { return e.Service != "" }

// ResolvedConsumer is the layer-agnostic view of one consumer.
type ResolvedConsumer struct {
	Decl    *ast.ConsumerDecl
	Package string
	Service string
	Name    string
	// Event is the contract this consumer handles. Zero-valued Contract
	// means the reference did not resolve.
	Event ResolvedEvent
	// Group is the broker identity this consumer joins: the Kafka
	// consumer group, the NATS queue group. Every target emits it
	// verbatim; [ConsumerGroup] is the only place it is decided.
	Group string
	Doc   []string
}

// HasEvents reports whether the project declares any event or consumer.
// Codegen skips the whole event pipeline when it does not.
func (p *Project) HasEvents() bool {
	if p == nil {
		return false
	}
	for _, pkg := range p.Packages {
		if pkg != nil && (len(pkg.Events) > 0 || len(pkg.Consumers) > 0) {
			return true
		}
	}
	return false
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

// Consumers returns every consumer declared in the project, ordered by
// package then name.
func (p *Project) Consumers() []ResolvedConsumer {
	if p == nil {
		return nil
	}
	var out []ResolvedConsumer
	for _, pkgName := range sortedNames(p.Packages) {
		pkg := p.Packages[pkgName]
		if pkg == nil {
			continue
		}
		for _, name := range sortedNames(pkg.Consumers) {
			ci := pkg.Consumers[name]
			out = append(out, p.ResolveConsumer(pkg, ci.Service, ci.Decl))
		}
	}
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
	ei, ok := pkg.Events[name]
	if !ok {
		return ResolvedEvent{}, false
	}
	return p.resolveEvent(pkg, ei), true
}

// resolveEvent computes the layer-agnostic facts for one event.
func (p *Project) resolveEvent(pkg *Package, ei *EventInfo) ResolvedEvent {
	d := ei.Decl
	re := ResolvedEvent{
		Decl:     d,
		Package:  pkg.Name,
		Service:  ei.Service,
		Name:     d.Name,
		Contract: ContractName(pkg.Name, d),
		Doc:      d.Doc,
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

// ResolveConsumer computes the layer-agnostic facts for one consumer
// declared by svcName in pkg.
func (p *Project) ResolveConsumer(pkg *Package, svcName string, d *ast.ConsumerDecl) ResolvedConsumer {
	if p == nil || pkg == nil || d == nil {
		return ResolvedConsumer{}
	}
	rc := ResolvedConsumer{
		Decl:    d,
		Package: pkg.Name,
		Service: svcName,
		Name:    d.Name,
		Group:   ConsumerGroup(pkg.Name, svcName, pkg.Services[svcName], d),
		Doc:     d.Doc,
	}
	if ev, ok := p.LookupEvent(pkg.Name, eventRefName(d.Event)); ok {
		rc.Event = ev
	}
	return rc
}

// ConsumerGroup returns the broker identity of one consumer: the
// `@consumerGroup` argument on the consumer, else the one on the service
// declaring it, else `<pkgName>-<svcName>-<Consumer>`. It is the only
// place the value is decided; targets read it off
// [ResolvedConsumer.Group].
//
// The separator is load-bearing. A NATS JetStream durable name may hold
// neither a dot nor whitespace, so a dotted derivation would be unusable
// as one. `@group` never reaches here: it decides where generated files
// land, not who the broker thinks is reading.
//
// On a transport that remembers a position per group - Kafka, and
// JetStream - the derived value moves when the package, service or
// consumer is renamed, and the new name has no position on the broker.
func ConsumerGroup(pkgName, svcName string, svc *ServiceInfo, d *ast.ConsumerDecl) string {
	if d == nil {
		return ""
	}
	if s, ok := DecoratorStringArg(d.Decorators, DecoratorConsumerGroup); ok {
		return s
	}
	if svc != nil && svc.Primary != nil {
		if s, ok := DecoratorStringArg(svc.Primary.Decorators, DecoratorConsumerGroup); ok {
			return s
		}
	}
	parts := make([]string, 0, 3)
	for _, part := range []string{pkgName, svcName, d.Name} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, "-")
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

// eventRefName renders a consumer's event reference, "" when absent.
func eventRefName(ce *ast.ConsumerEvent) string {
	if ce == nil || ce.Ref == nil || ce.Ref.Name == nil {
		return ""
	}
	return ce.Ref.Name.String()
}

// serviceMemberSite is one decorator-bearing member of a service body.
type serviceMemberSite struct {
	Level      Level
	Name       string
	Pos        lexer.Position
	Decorators []*ast.Decorator
}

// Label renders the diagnostic phrase for this site, e.g.
// "method Users.Create".
func (s serviceMemberSite) Label(svc string) string {
	return s.Level.Name() + " " + svc + "." + s.Name
}

// serviceMemberSites lists every decorator-bearing member of a service
// body, in source order. Every pass that walks a service's decorators
// reads this list.
func serviceMemberSites(d *ast.ServiceDecl) []serviceMemberSite {
	if d == nil {
		return nil
	}
	out := make([]serviceMemberSite, 0, len(d.Members))
	for _, m := range d.Members {
		switch v := m.(type) {
		case *ast.Method:
			out = append(out, serviceMemberSite{LvlMethod, v.Name, v.Pos, v.Decorators})
		case *ast.EventDecl:
			out = append(out, serviceMemberSite{LvlEvent, v.Name, v.Pos, v.Decorators})
		case *ast.ConsumerDecl:
			out = append(out, serviceMemberSite{LvlConsumer, v.Name, v.Pos, v.Decorators})
		}
	}
	return out
}
