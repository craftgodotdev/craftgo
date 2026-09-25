package semantic

import (
	"slices"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/prims"
)

// collectDecls fills the symbol tables and reports duplicate names. Types,
// enums, scalars and errors share one namespace; middlewares and events
// each have their own; a service's primary and extend blocks are collected
// under its name.
func (a *analyzer) collectDecls(files []*ast.File) {
	seen := map[string]lexer.Position{}   // type / enum / scalar / error namespace
	seenMW := map[string]lexer.Position{} // middleware namespace
	seenEv := map[string]lexer.Position{} // event namespace, package-wide
	// A middleware name never becomes a bare Go type, so it may be a built-in spelling.
	registerIn := func(table map[string]lexer.Position, name string, pos lexer.Position, rejectBuiltin bool) bool {
		if rejectBuiltin && prims.Is(name) {
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
			if d.DeclName() == "" {
				continue // the parser reported the missing name
			}
			switch dd := d.(type) {
			case *ast.TypeDecl:
				if registerIn(seen, dd.Name, dd.Pos, true) {
					a.pkg.Types[dd.Name] = dd
				}
			case *ast.EnumDecl:
				if registerIn(seen, dd.Name, dd.Pos, true) {
					a.pkg.Enums[dd.Name] = dd
				}
			case *ast.ErrorDecl:
				if registerIn(seen, dd.Name, dd.Pos, true) {
					a.pkg.Errors[dd.Name] = dd
				}
			case *ast.ScalarDecl:
				if registerIn(seen, dd.Name, dd.Pos, true) {
					a.pkg.Scalars[dd.Name] = dd
				}
			case *ast.MiddlewareDecl:
				if registerIn(seenMW, dd.Name, dd.Pos, false) {
					a.pkg.Middlewares[dd.Name] = dd
				}
			case *ast.EventDecl:
				if a.registerMember(seenEv, dd.Name, dd.Pos, CodeEventDuplicate,
					"duplicate event %q in package %q - a listener names an event by this identifier, so it must be unique across the package") {
					a.pkg.Events[dd.Name] = dd
				}
			case *ast.ServiceDecl:
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

// registerMember records key in table or reports it as a duplicate. format
// receives the name and its scope: the key's qualifier, else the package name.
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

// mergeServices fills each service's Methods. The decorators an extend
// block's methods inherit ([inheritedFrom]) are prepended to each of them.
func (a *analyzer) mergeServices() {
	for _, si := range a.pkg.Services {
		if si.Primary == nil {
			continue
		}
		si.Methods = append(si.Methods, si.Primary.Methods()...)
		for _, e := range si.Extends {
			inherited := inheritedFrom(e)
			for _, m := range e.Methods() {
				if len(inherited) > 0 {
					m.Decorators = append(slices.Clone(inherited), m.Decorators...)
				}
				si.Methods = append(si.Methods, m)
			}
		}
	}
}

// checkExtendOrphans reports every `extend service` block whose service
// has no primary declaration in this package.
func (a *analyzer) checkExtendOrphans() {
	for _, name := range a.pkg.ServiceNames() {
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
