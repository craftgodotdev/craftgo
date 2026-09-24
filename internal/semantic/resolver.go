package semantic

import "github.com/craftgodotdev/craftgo/internal/ast"

// Resolver is a project's symbol table keyed as references are spelled:
// the current package's declarations bare (`Order`), every other
// package's qualified (`shared.Order`). A nil *Resolver misses every lookup.
type Resolver struct {
	// Proj is the analysed project the tables were built from.
	Proj        *Project
	Types       map[string]*ast.TypeDecl
	Enums       map[string]*ast.EnumDecl
	Scalars     map[string]*ast.ScalarDecl
	Errors      map[string]*ast.ErrorDecl
	Middlewares map[string]*ast.MiddlewareDecl
}

// qualifiedTable collects one declaration kind from every package into a
// single lookup, keyed bare for currentPkg and qualified for the rest.
func qualifiedTable[T any](proj *Project, currentPkg string, pick func(*Package) map[string]T) map[string]T {
	if proj == nil {
		return nil
	}
	out := map[string]T{}
	for pkgName, p := range proj.Packages {
		if p == nil {
			continue
		}
		for name, decl := range pick(p) {
			if pkgName == "" || pkgName == currentPkg {
				out[name] = decl
				continue
			}
			out[pkgName+"."+name] = decl
		}
	}
	return out
}

// NewResolver builds the tables for currentPkg. A nil project yields a
// usable resolver whose lookups all miss.
func NewResolver(proj *Project, currentPkg string) *Resolver {
	return &Resolver{
		Proj:        proj,
		Types:       qualifiedTable(proj, currentPkg, func(p *Package) map[string]*ast.TypeDecl { return p.Types }),
		Enums:       qualifiedTable(proj, currentPkg, func(p *Package) map[string]*ast.EnumDecl { return p.Enums }),
		Scalars:     qualifiedTable(proj, currentPkg, func(p *Package) map[string]*ast.ScalarDecl { return p.Scalars }),
		Errors:      qualifiedTable(proj, currentPkg, func(p *Package) map[string]*ast.ErrorDecl { return p.Errors }),
		Middlewares: qualifiedTable(proj, currentPkg, func(p *Package) map[string]*ast.MiddlewareDecl { return p.Middlewares }),
	}
}

// ResolverFor returns r, or a resolver over pkg alone when r is nil.
func ResolverFor(pkg *Package, r *Resolver) *Resolver {
	if r != nil {
		return r
	}
	return NewResolver(&Project{Packages: map[string]*Package{pkg.Name: pkg}}, pkg.Name)
}

// Project returns the analysed project, or nil on a nil receiver.
func (r *Resolver) Project() *Project {
	if r == nil {
		return nil
	}
	return r.Proj
}

// LookupType returns the type name spells (bare or `pkg.Name`), or nil.
func (r *Resolver) LookupType(name string) *ast.TypeDecl {
	if r == nil {
		return nil
	}
	return r.Types[name]
}

// LookupEnum is the enum counterpart of [Resolver.LookupType].
func (r *Resolver) LookupEnum(name string) *ast.EnumDecl {
	if r == nil {
		return nil
	}
	return r.Enums[name]
}

// LookupScalar is the scalar counterpart of [Resolver.LookupType].
func (r *Resolver) LookupScalar(name string) *ast.ScalarDecl {
	if r == nil {
		return nil
	}
	return r.Scalars[name]
}

// LookupError is the error counterpart of [Resolver.LookupType].
func (r *Resolver) LookupError(name string) *ast.ErrorDecl {
	if r == nil {
		return nil
	}
	return r.Errors[name]
}

// LookupMiddleware is the middleware counterpart of [Resolver.LookupType].
func (r *Resolver) LookupMiddleware(name string) *ast.MiddlewareDecl {
	if r == nil {
		return nil
	}
	return r.Middlewares[name]
}
