// Symbol-table population + cross-file package-name check + extend-service merge.
package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

func (a *analyzer) checkPackageName(files []*ast.File) {
	var name string
	var firstPos lexer.Position
	for _, f := range files {
		if f.Package == nil {
			continue
		}
		if name == "" {
			name = f.Package.Name
			firstPos = f.Package.Pos
			continue
		}
		if name != f.Package.Name {
			d := a.diag(f.Package.Pos, f.Package.Pos, lexer.SeverityError,
				CodePackageMismatch,
				"package name %q conflicts with %q", f.Package.Name, name)
			d.Related = related(firstPos, "first declared here")
		}
	}
	a.pkg.Name = name
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
func (a *analyzer) collectDecls(files []*ast.File) {
	seen := map[string]lexer.Position{}   // type / enum / scalar / error namespace
	seenMW := map[string]lexer.Position{} // middleware namespace
	registerIn := func(table map[string]lexer.Position, name string, pos lexer.Position, rejectBuiltin bool) bool {
		if rejectBuiltin && builtinTypes[name] {
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
			}
		}
	}
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
			if !a.opts.skipExtendOrphanCheck {
				for _, e := range si.Extends {
					a.diag(e.Pos, e.Pos, lexer.SeverityError, CodeServiceExtendOrphan,
						"extend service %q has no primary declaration", name)
				}
			}
			continue
		}
		si.Methods = append(si.Methods, si.Primary.Methods()...)
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
				if len(propagate) > 0 {
					merged := make([]*ast.Decorator, 0, len(propagate)+len(m.Decorators))
					for _, src := range propagate {
						// Clone so the Propagated flag does not leak
						// into the original extend block's decorator
						// list (which other passes still read).
						cp := *src
						cp.Propagated = true
						merged = append(merged, &cp)
					}
					merged = append(merged, m.Decorators...)
					m.Decorators = merged
				}
				si.Methods = append(si.Methods, m)
			}
		}
	}
}
