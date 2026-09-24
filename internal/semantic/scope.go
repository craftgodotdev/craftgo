package semantic

import (
	"maps"
	"slices"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

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
