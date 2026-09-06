package semantic

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// DeclKind selects which declaration tables [Package.Decl], [Package.Decls]
// and [Project.Lookup] search. Kinds combine as a bit set.
type DeclKind uint8

const (
	TypeDecls DeclKind = 1 << iota
	EnumDecls
	ScalarDecls
	ErrorDecls
	MiddlewareDecls
	ServiceDecls // primary `service` declarations

	AnyDecl = TypeDecls | EnumDecls | ScalarDecls | ErrorDecls | MiddlewareDecls | ServiceDecls
	// TypeShapeDecls is every kind a type-shape position (a field type, a
	// mixin, a request or response, a generic argument) can name.
	TypeShapeDecls = AnyDecl &^ MiddlewareDecls
)

// Decl returns the declaration of name among the tables kinds selects, or
// nil. Middleware names live in their own namespace, so a name may be both
// a middleware and another kind; kinds decides which one is meant.
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
	if kinds&ServiceDecls != 0 {
		if si := p.Services[name]; si != nil && si.Primary != nil {
			return si.Primary
		}
	}
	return nil
}

// Decls returns every declaration of the selected kinds, ordered by kind
// (type, enum, scalar, error, middleware, service) and then by name.
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
	if kinds&ServiceDecls != 0 {
		for _, name := range sortedNames(p.Services) {
			if sd := p.Services[name].Primary; sd != nil {
				out = append(out, sd)
			}
		}
	}
	return out
}

func appendDecls[D ast.Decl](out []ast.Decl, table map[string]D) []ast.Decl {
	for _, name := range sortedNames(table) {
		out = append(out, table[name])
	}
	return out
}

// Lookup resolves name to its declaration among the selected kinds: a
// qualified `pkg.Name` in package pkg only, a bare name in homePkg first
// and then in any other package (by name), so a sibling package's
// declaration is reachable without its qualifier. Nil when nothing
// matches.
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
	for _, pkgName := range sortedNames(p.Packages) {
		if pkgName == homePkg {
			continue
		}
		if d := p.Packages[pkgName].Decl(name, kinds); d != nil {
			return d
		}
	}
	return nil
}
