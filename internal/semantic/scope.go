package semantic

import (
	"sort"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// packageNamed returns the project package declared as `package name`,
// or nil when the project has none.
func (a *analyzer) packageNamed(name string) *Package {
	if a.proj == nil {
		if a.pkg != nil && a.pkg.Name == name {
			return a.pkg
		}
		return nil
	}
	return a.proj.Packages[name]
}

// projectDeclared reports whether name resolves to a declaration has
// recognises: a qualified `pkg.Name` must resolve in that package, a bare
// name in any package of the project.
func (a *analyzer) projectDeclared(name string, has func(*Package, string) bool) bool {
	if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
		pkg := a.packageNamed(name[:dot])
		return pkg != nil && has(pkg, name[dot+1:])
	}
	if a.proj == nil {
		return a.pkg != nil && has(a.pkg, name)
	}
	for _, p := range a.proj.Packages {
		if p != nil && has(p, name) {
			return true
		}
	}
	return false
}

// middlewareDeclared reports whether name (bare or `pkg.Name`) is a
// declared middleware.
func (a *analyzer) middlewareDeclared(name string) bool {
	return a.projectDeclared(name, func(p *Package, n string) bool {
		_, ok := p.Middlewares[n]
		return ok
	})
}

// errorDeclared reports whether name (bare or `pkg.Name`) is a declared
// error type.
func (a *analyzer) errorDeclared(name string) bool {
	return a.projectDeclared(name, func(p *Package, n string) bool {
		_, ok := p.Errors[n]
		return ok
	})
}

// sortedNames returns the keys of m in alphabetical order so a pass emits
// its diagnostics in a stable order.
func sortedNames[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// primaryServiceElsewhere returns the package and declaration of a
// primary `service name` declared outside the analyser's own package,
// or ("", nil) when no other package declares one.
func (a *analyzer) primaryServiceElsewhere(name string) (string, *ast.ServiceDecl) {
	if a.proj == nil {
		return "", nil
	}
	for _, pkgName := range sortedNames(a.proj.Packages) {
		pkg := a.proj.Packages[pkgName]
		if pkg == nil || pkg == a.pkg {
			continue
		}
		if si := pkg.Services[name]; si != nil && si.Primary != nil {
			return pkgName, si.Primary
		}
	}
	return "", nil
}

// refHome splits a type reference into the package it resolves in and
// the symbol name: a bare name lives in the analyser's own package, a
// qualified `pkg.Name` in pkg. ok is false for a reference with more
// than one qualifier.
func (a *analyzer) refHome(n *ast.QualifiedIdent) (pkgName, name string, ok bool) {
	if n == nil || len(n.Parts) == 0 || len(n.Parts) > 2 {
		return "", "", false
	}
	if len(n.Parts) == 2 {
		return n.Parts[0], n.Parts[1], true
	}
	return a.pkg.Name, n.Parts[0], true
}

// refDisplay renders a resolved (package, name) pair the way the source
// spells it: bare inside the analyser's own package, qualified elsewhere.
func (a *analyzer) refDisplay(pkgName, name string) string {
	if pkgName == a.pkg.Name {
		return name
	}
	return pkgName + "." + name
}
