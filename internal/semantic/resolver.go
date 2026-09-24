package semantic

import "github.com/craftgodotdev/craftgo/internal/ast"

// Resolver looks up declarations by name as one package spells them: its own
// bare (`Order`), another package's qualified (`shared.Order`). A nil
// *Resolver misses every lookup.
type Resolver struct {
	proj    *Project
	current string
}

// NewResolver returns the resolver of package current in proj; a nil proj
// misses every lookup.
func NewResolver(proj *Project, current string) *Resolver {
	return &Resolver{proj: proj, current: current}
}

// PackageResolver returns the resolver of pkg in a project holding pkg alone.
func PackageResolver(pkg *Package) *Resolver {
	return NewResolver(&Project{Packages: map[string]*Package{pkg.Name: pkg}}, pkg.Name)
}

// Project returns the analysed project, or nil on a nil receiver.
func (r *Resolver) Project() *Project {
	if r == nil {
		return nil
	}
	return r.proj
}

// LookupType returns the type name spells (bare or `pkg.Name`), or nil.
func (r *Resolver) LookupType(name string) *ast.TypeDecl {
	d, _ := r.lookup(name, TypeDecls).(*ast.TypeDecl)
	return d
}

// LookupEnum is the enum counterpart of [Resolver.LookupType].
func (r *Resolver) LookupEnum(name string) *ast.EnumDecl {
	d, _ := r.lookup(name, EnumDecls).(*ast.EnumDecl)
	return d
}

// LookupScalar is the scalar counterpart of [Resolver.LookupType].
func (r *Resolver) LookupScalar(name string) *ast.ScalarDecl {
	d, _ := r.lookup(name, ScalarDecls).(*ast.ScalarDecl)
	return d
}

// lookup returns the declaration of the selected kinds name spells, or nil.
func (r *Resolver) lookup(name string, kinds DeclKind) ast.Decl {
	if r == nil {
		return nil
	}
	pkg, sym := r.proj.resolveName(r.current, name)
	if pkg == nil {
		return nil
	}
	return pkg.Decl(sym, kinds)
}
