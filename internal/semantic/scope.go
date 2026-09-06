package semantic

import "strings"

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
