package semantic

import (
	"sort"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// packageNamed returns the project's package called name, or nil.
func (a *analyzer) packageNamed(name string) *Package {
	return a.proj.Packages[name]
}

// middlewareDeclared reports whether name, bare or `pkg.Name`, is a declared middleware.
func (a *analyzer) middlewareDeclared(name string) bool {
	return a.proj.Lookup(a.pkg.Name, name, MiddlewareDecls) != nil
}

// errorDeclared reports whether name, bare or `pkg.Name`, is a declared error.
func (a *analyzer) errorDeclared(name string) bool {
	return a.proj.Lookup(a.pkg.Name, name, ErrorDecls) != nil
}

// sortedNames returns the keys of m in sorted order.
func sortedNames[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// primaryServiceElsewhere returns the package and declaration of a primary
// `service name` in another package, or ("", nil).
func (a *analyzer) primaryServiceElsewhere(name string) (string, *ast.ServiceDecl) {
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

// refHome splits a reference into its package (the analyser's own for a
// bare name) and symbol; ok is false for more than one qualifier.
func (a *analyzer) refHome(n *ast.QualifiedIdent) (pkgName, name string, ok bool) {
	if n == nil || len(n.Parts) == 0 || len(n.Parts) > 2 {
		return "", "", false
	}
	if len(n.Parts) == 2 {
		return n.Parts[0], n.Parts[1], true
	}
	return a.pkg.Name, n.Parts[0], true
}

// refDisplay spells (pkgName, name) bare in the analyser's own package and
// qualified elsewhere.
func (a *analyzer) refDisplay(pkgName, name string) string {
	if pkgName == a.pkg.Name {
		return name
	}
	return pkgName + "." + name
}

// resolveNamed returns the package n resolves in (homePkg for a bare name)
// and the symbol it names; the package is nil when the project has none.
func (a *analyzer) resolveNamed(homePkg string, n *ast.NamedTypeRef) (*Package, string) {
	if n == nil || n.Name == nil {
		return nil, ""
	}
	switch parts := n.Name.Parts; len(parts) {
	case 1:
		return a.packageNamed(homePkg), parts[0]
	case 2:
		return a.packageNamed(parts[0]), parts[1]
	}
	return nil, ""
}

// lookupScalarIn returns the scalar n names, a bare name resolving in
// homePkg, or nil.
func (a *analyzer) lookupScalarIn(homePkg string, n *ast.NamedTypeRef) *ast.ScalarDecl {
	pkg, sym := a.resolveNamed(homePkg, n)
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
	pkg, sym := a.resolveNamed(homePkg, n)
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
