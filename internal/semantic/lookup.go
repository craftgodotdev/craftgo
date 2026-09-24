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
	// TypeShapeDecls leaves out the kinds that never name a type shape.
	TypeShapeDecls = AnyDecl &^ MiddlewareDecls &^ EventDecls
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

// PackageNames returns the names of p's named packages, sorted; the package
// of files without a `package` clause is left out.
func (p *Project) PackageNames() []string {
	return slices.DeleteFunc(slices.Sorted(maps.Keys(p.Packages)), func(name string) bool { return name == "" })
}

// Lookup returns the declaration name refers to among the selected kinds,
// or nil: a qualified `pkg.Name` in pkg only, a bare name in homePkg, then
// in the other packages in name order.
func (p *Project) Lookup(homePkg, name string, kinds DeclKind) ast.Decl {
	if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
		if pkg := p.Packages[name[:dot]]; pkg != nil {
			return pkg.Decl(name[dot+1:], kinds)
		}
		return nil
	}
	if pkg := p.Packages[homePkg]; pkg != nil {
		if d := pkg.Decl(name, kinds); d != nil {
			return d
		}
	}
	for _, pkgName := range slices.Sorted(maps.Keys(p.Packages)) {
		if pkgName == homePkg {
			continue
		}
		if d := p.Packages[pkgName].Decl(name, kinds); d != nil {
			return d
		}
	}
	return nil
}
