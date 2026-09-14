package semantic

import (
	"sort"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// packageNamed returns the project package declared as `package name`,
// or nil when the project has none.
func (a *analyzer) packageNamed(name string) *Package {
	return a.proj.Packages[name]
}

// middlewareDeclared reports whether name (bare or `pkg.Name`) is a
// declared middleware.
func (a *analyzer) middlewareDeclared(name string) bool {
	return a.proj.Lookup(a.pkg.Name, name, MiddlewareDecls) != nil
}

// consumeMiddlewareDeclared reports whether name (bare or `pkg.Name`) is
// a declared `consume middleware`.
func (a *analyzer) consumeMiddlewareDeclared(name string) bool {
	return a.proj.Lookup(a.pkg.Name, name, ConsumeMiddlewareDecls) != nil
}

// errorDeclared reports whether name (bare or `pkg.Name`) is a declared
// error type.
func (a *analyzer) errorDeclared(name string) bool {
	return a.proj.Lookup(a.pkg.Name, name, ErrorDecls) != nil
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

// resolveNamed returns the package a named type reference resolves in and
// the symbol it names: a bare name resolves in homePkg, a qualified
// `pkg.Name` in pkg. The package is nil when the project declares none
// by that name.
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

// lookupScalarIn returns the scalar declaration n names - a bare name
// resolved in homePkg, a qualified `pkg.Name` in pkg - or nil.
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

// primOf returns the primitive a non-collection named type lowers to:
// the primitive behind a scalar (bare or qualified), otherwise the name
// as spelled. "" when t is not a named type.
func (a *analyzer) primOf(t *ast.TypeRef) string {
	if t == nil || t.Named == nil || t.Named.Name == nil {
		return ""
	}
	if sd := a.lookupScalar(t.Named); sd != nil {
		return sd.Primitive
	}
	return t.Named.Name.String()
}
