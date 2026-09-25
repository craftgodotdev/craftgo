package semantic

import (
	"maps"
	"slices"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// DeclKind is a bit set of declaration kinds to search.
type DeclKind uint8

const (
	TypeDecls DeclKind = 1 << iota
	EnumDecls
	ScalarDecls
	ErrorDecls
	MiddlewareDecls
	ServiceDecls // primary `service` declarations
	EventDecls

	AnyDecl = TypeDecls | EnumDecls | ScalarDecls | ErrorDecls | MiddlewareDecls | ServiceDecls | EventDecls
	// TypeRefDecls are the kinds a type reference may name.
	TypeRefDecls = TypeDecls | EnumDecls | ScalarDecls
)

// Decl returns the declaration of name among the selected kinds, or nil.
// Kinds are searched in [Package.Decls] order; the first match wins.
func (p *Package) Decl(name string, kinds DeclKind) ast.Decl {
	if kinds&TypeDecls != 0 {
		if d, ok := p.Types[name]; ok {
			return d
		}
	}
	if kinds&EnumDecls != 0 {
		if d, ok := p.Enums[name]; ok {
			return d
		}
	}
	if kinds&ScalarDecls != 0 {
		if d, ok := p.Scalars[name]; ok {
			return d
		}
	}
	if kinds&ErrorDecls != 0 {
		if d, ok := p.Errors[name]; ok {
			return d
		}
	}
	if kinds&MiddlewareDecls != 0 {
		if d, ok := p.Middlewares[name]; ok {
			return d
		}
	}
	if kinds&EventDecls != 0 {
		if d, ok := p.Events[name]; ok {
			return d
		}
	}
	if kinds&ServiceDecls != 0 {
		if si := p.Services[name]; si != nil && si.Primary != nil {
			return si.Primary
		}
	}
	return nil
}

// Decls returns every declaration of the selected kinds, ordered by kind
// (type, enum, scalar, error, middleware, event, service), then by name.
func (p *Package) Decls(kinds DeclKind) []ast.Decl {
	var out []ast.Decl
	if kinds&TypeDecls != 0 {
		out = appendDecls(out, p.Types)
	}
	if kinds&EnumDecls != 0 {
		out = appendDecls(out, p.Enums)
	}
	if kinds&ScalarDecls != 0 {
		out = appendDecls(out, p.Scalars)
	}
	if kinds&ErrorDecls != 0 {
		out = appendDecls(out, p.Errors)
	}
	if kinds&MiddlewareDecls != 0 {
		out = appendDecls(out, p.Middlewares)
	}
	if kinds&EventDecls != 0 {
		out = appendDecls(out, p.Events)
	}
	if kinds&ServiceDecls != 0 {
		for _, name := range p.ServiceNames() {
			if sd := p.Services[name].Primary; sd != nil {
				out = append(out, sd)
			}
		}
	}
	return out
}

func appendDecls[D ast.Decl](out []ast.Decl, table map[string]D) []ast.Decl {
	for _, name := range slices.Sorted(maps.Keys(table)) {
		out = append(out, table[name])
	}
	return out
}

// ServiceNames returns the names of p's services, sorted.
func (p *Package) ServiceNames() []string {
	return slices.Sorted(maps.Keys(p.Services))
}

// PackageNames returns the names of p's packages, sorted.
func (p *Project) PackageNames() []string {
	return slices.Sorted(maps.Keys(p.Packages))
}

// resolve returns the package q names a declaration of and that
// declaration's name: a bare name resolves in home, `pkg.Name` in pkg. The
// package is nil when the project has none of that name, or q has more
// qualifiers.
func (p *Project) resolve(home string, q *ast.QualifiedIdent) (*Package, string) {
	if p == nil || q == nil {
		return nil, ""
	}
	switch len(q.Parts) {
	case 1:
		return p.Packages[home], q.Parts[0]
	case 2:
		return p.Packages[q.Parts[0]], q.Parts[1]
	}
	return nil, ""
}

// resolveName is [Project.resolve] for a reference spelled as text.
func (p *Project) resolveName(home, ref string) (*Package, string) {
	return p.resolve(home, &ast.QualifiedIdent{Parts: strings.Split(ref, ".")})
}

// projectWideDecls are the kinds a bare name finds in any package: a
// middleware or an error its own package does not declare.
const projectWideDecls = MiddlewareDecls | ErrorDecls

// Lookup returns the declaration name refers to among the selected kinds,
// or nil: a qualified `pkg.Name` in pkg only, a bare name in homePkg, then,
// for [projectWideDecls], in the other packages in name order.
func (p *Project) Lookup(homePkg, name string, kinds DeclKind) ast.Decl {
	if pkg, sym := p.resolveName(homePkg, name); pkg != nil {
		if d := pkg.Decl(sym, kinds); d != nil {
			return d
		}
	}
	if kinds&projectWideDecls == 0 || strings.Contains(name, ".") {
		return nil
	}
	for _, pkgName := range p.PackageNames() {
		if pkgName == homePkg {
			continue
		}
		if d := p.Packages[pkgName].Decl(name, kinds&projectWideDecls); d != nil {
			return d
		}
	}
	return nil
}

// middlewareDeclared reports whether name, bare or `pkg.Name`, is a declared middleware.
func (a *analyzer) middlewareDeclared(name string) bool {
	return a.proj.Lookup(a.pkg.Name, name, MiddlewareDecls) != nil
}

// errorDeclared reports whether name, bare or `pkg.Name`, is a declared error.
func (a *analyzer) errorDeclared(name string) bool {
	return a.proj.Lookup(a.pkg.Name, name, ErrorDecls) != nil
}

// primaryServiceElsewhere returns the package and declaration of a primary
// `service name` in another package, or ("", nil).
func (a *analyzer) primaryServiceElsewhere(name string) (string, *ast.ServiceDecl) {
	for _, pkgName := range slices.Sorted(maps.Keys(a.proj.Packages)) {
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

// refDisplay spells (pkgName, name) bare in the analyser's own package and
// qualified elsewhere.
func (a *analyzer) refDisplay(pkgName, name string) string {
	if pkgName == a.pkg.Name {
		return name
	}
	return pkgName + "." + name
}

// lookupScalarIn returns the scalar n names, a bare name resolving in
// homePkg, or nil.
func (a *analyzer) lookupScalarIn(homePkg string, n *ast.NamedTypeRef) *ast.ScalarDecl {
	if n == nil {
		return nil
	}
	pkg, sym := a.proj.resolve(homePkg, n.Name)
	if pkg == nil {
		return nil
	}
	return pkg.Scalars[sym]
}

// lookupScalar is [analyzer.lookupScalarIn] for the analyser's own package.
func (a *analyzer) lookupScalar(n *ast.NamedTypeRef) *ast.ScalarDecl {
	return a.lookupScalarIn(a.pkg.Name, n)
}

// lookupEnumIn is the enum counterpart of [analyzer.lookupScalarIn].
func (a *analyzer) lookupEnumIn(homePkg string, n *ast.NamedTypeRef) *ast.EnumDecl {
	if n == nil {
		return nil
	}
	pkg, sym := a.proj.resolve(homePkg, n.Name)
	if pkg == nil {
		return nil
	}
	return pkg.Enums[sym]
}

// lookupEnum is [analyzer.lookupEnumIn] for the analyser's own package.
func (a *analyzer) lookupEnum(n *ast.NamedTypeRef) *ast.EnumDecl {
	return a.lookupEnumIn(a.pkg.Name, n)
}

// primOf returns the primitive of the scalar t names, else t's name as
// spelled; "" when t is not a named type.
func (a *analyzer) primOf(t *ast.TypeRef) string {
	if t == nil || t.Named == nil || t.Named.Name == nil {
		return ""
	}
	if sd := a.lookupScalar(t.Named); sd != nil {
		return sd.Primitive
	}
	return t.Named.Name.String()
}

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
