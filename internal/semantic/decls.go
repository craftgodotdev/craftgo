// Symbol-table population + package name + extend-service merge.
package semantic

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/prims"
)

// setPackageName records the `package X` name the group's files declare;
// a group without a declaration keeps the empty name.
func (a *analyzer) setPackageName(files []*ast.File) {
	for _, f := range files {
		if f.Package != nil {
			a.pkg.Name = f.Package.Name
			return
		}
	}
}

// collectDecls walks every declaration once, populates the Package symbol
// tables, and reports duplicate top-level names. Services are special-cased:
// they merge across files via [ServiceInfo] (see [mergeServices]).
//
// Namespace separation matches the codegen output packages:
//
//   - type / enum / scalar / error → all emit into the types package,
//     so they share one `seen` map. A duplicate name across kinds is
//     a hard collision in the generated Go.
//   - middleware → emits into its own package (svccontext aliases +
//     middleware impl pkg), independent from types. Uses a separate
//     `seenMW` map so `middleware Foo` and `type Foo` coexist.
//   - service → handler / route packages, each namespaced per
//     service; merge handled by mergeServices.
//   - event / consumer → their own namespaces, so `event OrderPlaced`
//     may sit next to the `type OrderPlaced` it carries.
func (a *analyzer) collectDecls(files []*ast.File) {
	seen := map[string]lexer.Position{}    // type / enum / scalar / error namespace
	seenMW := map[string]lexer.Position{}  // middleware namespace
	seenEv := map[string]lexer.Position{}  // event namespace, package-wide
	seenCon := map[string]lexer.Position{} // consumer namespace, per service
	registerIn := func(table map[string]lexer.Position, name string, pos lexer.Position, rejectBuiltin bool) bool {
		if rejectBuiltin && prims.Is(name) {
			// A type / enum / scalar / error named after a built-in spelling
			// (`int`, `string`, `any`, ...) lowers to a Go type that shadows
			// the built-in and fails to compile. (Middleware names live in a
			// separate Go namespace, so they are exempt.)
			a.diag(pos, pos, lexer.SeverityError, CodeDeclBuiltinName,
				"declaration name %q collides with a built-in type - it would shadow the built-in in the generated Go; choose a different name", name)
			return false
		}
		if prev, ok := table[name]; ok {
			d := a.diag(pos, pos, lexer.SeverityError, CodeDuplicateDecl,
				"duplicate top-level declaration %q", name)
			d.Related = related(prev, "first declared here")
			return false
		}
		table[name] = pos
		return true
	}
	for _, f := range files {
		for _, d := range f.Decls {
			// Defensive: the parser drops typed-nil pointers before
			// they reach the AST, but mid-typing edits in the LSP can
			// surface a nil decl here. Skip it rather than dereference.
			if d == nil {
				continue
			}
			switch dd := d.(type) {
			case *ast.TypeDecl:
				if dd == nil {
					continue
				}
				if registerIn(seen, dd.Name, dd.Pos, true) {
					a.pkg.Types[dd.Name] = dd
				}
			case *ast.EnumDecl:
				if dd == nil {
					continue
				}
				if registerIn(seen, dd.Name, dd.Pos, true) {
					a.pkg.Enums[dd.Name] = dd
				}
			case *ast.ErrorDecl:
				if dd == nil {
					continue
				}
				if registerIn(seen, dd.Name, dd.Pos, true) {
					a.pkg.Errors[dd.Name] = dd
				}
			case *ast.ScalarDecl:
				if dd == nil {
					continue
				}
				if registerIn(seen, dd.Name, dd.Pos, true) {
					a.pkg.Scalars[dd.Name] = dd
				}
			case *ast.MiddlewareDecl:
				if dd == nil {
					continue
				}
				if registerIn(seenMW, dd.Name, dd.Pos, false) {
					a.pkg.Middlewares[dd.Name] = dd
				}
			case *ast.EventDecl:
				if dd == nil {
					continue
				}
				// A contract declared outside a service shares the event
				// namespace with the ones services declare; it simply has
				// no producer in this design.
				if a.registerMember(seenEv, dd.Name, dd.Pos, CodeEventDuplicate,
					"duplicate event %q in package %q - a consumer names an event by this identifier, so it must be unique across the package") {
					a.pkg.Events[dd.Name] = &EventInfo{Decl: dd}
				}
			case *ast.ServiceDecl:
				if dd == nil {
					continue
				}
				si, ok := a.pkg.Services[dd.Name]
				if !ok {
					si = &ServiceInfo{}
					a.pkg.Services[dd.Name] = si
				}
				if dd.Extend {
					si.Extends = append(si.Extends, dd)
				} else if si.Primary != nil {
					d := a.diag(dd.Pos, dd.Pos, lexer.SeverityError, CodeServiceDuplicate,
						"duplicate primary service %q", dd.Name)
					d.Related = related(si.Primary.Pos, "first declared here")
				} else {
					si.Primary = dd
				}
				for _, ev := range dd.Events() {
					if a.registerMember(seenEv, ev.Name, ev.Pos, CodeEventDuplicate,
						"duplicate event %q in package %q - a consumer names an event by this identifier, so it must be unique across the package") {
						a.pkg.Events[ev.Name] = &EventInfo{Decl: ev, Service: dd.Name}
					}
				}
				for _, c := range dd.Consumers() {
					// Keyed per service because each service's stubs land
					// in its own folder. The name also feeds the derived
					// consumer group, which checkConsumerGroups checks
					// across the project.
					key := dd.Name + "." + c.Name
					if a.registerMember(seenCon, key, c.Pos, CodeConsumerDuplicateName,
						"duplicate consumer %q in service %q") {
						a.pkg.Consumers[key] = &ConsumerInfo{Decl: c, Service: dd.Name}
					}
				}
			}
		}
	}
}

// registerMember records a service-body member in its namespace table,
// reporting a duplicate against the first occurrence. format takes the
// member name and the scope it must be unique within.
func (a *analyzer) registerMember(table map[string]lexer.Position, key string, pos lexer.Position, code, format string) bool {
	if prev, dup := table[key]; dup {
		name, scope := key, a.pkg.Name
		if dot := strings.LastIndexByte(key, '.'); dot >= 0 {
			name, scope = key[dot+1:], key[:dot]
		}
		d := a.diag(pos, pos, lexer.SeverityError, code, format, name, scope)
		d.Related = related(prev, "first declared here")
		return false
	}
	table[key] = pos
	return true
}

// mergeServices flattens each [ServiceInfo] into a single ordered method
// list. Decorators on an `extend service` block are propagated to every
// method inside that block by prepending them to the method's own
// decorator chain - so a method authored under `@middlewares(Auth)
// extend service Users { ... }` sees Auth as if the decorator were
// written directly above it. This lets the same logical service split
// into "public" + "authenticated" sub-blocks without forking the
// service declaration.
func (a *analyzer) mergeServices() {
	for name, si := range a.pkg.Services {
		if si.Primary == nil {
			continue
		}
		si.Methods = append(si.Methods, si.Primary.Methods()...)
		si.Events = append(si.Events, si.Primary.Events()...)
		si.Consumers = append(si.Consumers, si.Primary.Consumers()...)
		for _, e := range si.Extends {
			// Filter decorators by level: only those that can apply at
			// method-level get propagated. Service-only decorators like
			// `@prefix` make no sense per-method - we emit a diagnostic
			// instead so the user moves them to the primary service.
			var propagate []*ast.Decorator
			for _, d := range e.Decorators {
				spec, ok := Lookup(d.Name)
				if !ok {
					// Unknown decorator: skip here; the decorator-check
					// pass already emits a diagnostic for it.
					continue
				}
				if d.Name == "group" {
					// @group on an extend block sets that block's codegen
					// output path + OpenAPI tag (consumed in codegen via
					// ServiceInfo.Extends so each block can nest under its
					// own folder). It is not a method decorator, so it is
					// neither rejected nor propagated onto the methods - but
					// the args pass skips extend decorators, so validate it
					// here.
					a.checkGroupArg(d)
					continue
				}
				if spec.Levels&LvlMethod == 0 {
					a.diag(d.Pos, d.Pos, lexer.SeverityError, CodeExtendDecoratorNotMethod,
						"decorator @%s on extend service %q is not valid at method level; move it to the primary service", d.Name, name)
					continue
				}
				propagate = append(propagate, d)
			}
			for _, m := range e.Methods() {
				m.Decorators = prependPropagated(propagate, m.Decorators)
				si.Methods = append(si.Methods, m)
			}
			// Method-level decorators mean nothing on an event or a
			// consumer, so an extend block's chain is not propagated
			// onto them; the members themselves still merge in.
			si.Events = append(si.Events, e.Events()...)
			si.Consumers = append(si.Consumers, e.Consumers()...)
		}
	}
}

// prependPropagated puts an extend block's decorators in front of a
// member's own chain. Each is cloned so the Propagated flag does not leak
// into the block's list, which other passes still read.
func prependPropagated(propagate, own []*ast.Decorator) []*ast.Decorator {
	if len(propagate) == 0 {
		return own
	}
	merged := make([]*ast.Decorator, 0, len(propagate)+len(own))
	for _, src := range propagate {
		cp := *src
		cp.Propagated = true
		merged = append(merged, &cp)
	}
	return append(merged, own...)
}

// checkExtendOrphans reports every `extend service` block whose service
// has no primary declaration in this package. When the primary lives in
// a sibling package the message names it: extend declarations are
// per-package.
func (a *analyzer) checkExtendOrphans() {
	for _, name := range sortedNames(a.pkg.Services) {
		si := a.pkg.Services[name]
		if si == nil || si.Primary != nil {
			continue
		}
		otherPkg, primary := a.primaryServiceElsewhere(name)
		for _, e := range si.Extends {
			if primary == nil {
				a.diag(e.Pos, e.Pos, lexer.SeverityError, CodeServiceExtendOrphan,
					"extend service %q has no primary declaration", name)
				continue
			}
			d := a.diag(e.Pos, e.Pos, lexer.SeverityError, CodeServiceExtendOrphan,
				"extend service %q: primary lives in package %q - extend declarations are per-package, move this block into that package or rename the service",
				name, otherPkg)
			d.Related = related(primary.Pos, "primary service declared here")
		}
	}
}
