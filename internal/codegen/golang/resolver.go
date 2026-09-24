package golang

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// projectResolver resolves bare and package-qualified names for one generated
// package and maps the other packages to Go import paths; a nil one always misses.
type projectResolver struct {
	*semantic.Resolver
	CrossPkg crossPkg
}

// buildProjectResolver returns the resolver for currentPkgName; it is non-nil
// even for a nil proj.
func buildProjectResolver(proj *semantic.Project, cfg *config.Config, currentPkgName string) *projectResolver {
	return &projectResolver{
		Resolver: semantic.NewResolver(proj, currentPkgName),
		CrossPkg: buildCrossPkg(proj, cfg, currentPkgName),
	}
}

// ImportPath returns the Go import path of DSL package pkgAlias, or "" when
// it is unknown.
func (r *projectResolver) ImportPath(pkgAlias string) string {
	if r == nil || r.CrossPkg == nil {
		return ""
	}
	return r.CrossPkg[pkgAlias]
}

// QualifierFor returns the Go qualifier (`shared.`) and import path of a
// package-qualified ref, or two empty strings for any other.
func (r *projectResolver) QualifierFor(n *ast.NamedTypeRef) (string, string) {
	if r == nil || n == nil || n.Name == nil {
		return "", ""
	}
	parts := n.Name.Parts
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0] + ".", r.ImportPath(parts[0])
}

// resolverFor returns r, or a resolver over pkg alone when the caller has
// no project context.
func resolverFor(pkg *semantic.Package, r *projectResolver) *projectResolver {
	if r != nil {
		return r
	}
	return &projectResolver{Resolver: semantic.ResolverFor(pkg, nil)}
}

// The forwarders below keep a nil *projectResolver usable: a promoted method
// would dereference it to reach the embedded Resolver.

func (r *projectResolver) LookupType(name string) *ast.TypeDecl {
	if r == nil {
		return nil
	}
	return r.Resolver.LookupType(name)
}

func (r *projectResolver) LookupEnum(name string) *ast.EnumDecl {
	if r == nil {
		return nil
	}
	return r.Resolver.LookupEnum(name)
}

func (r *projectResolver) LookupScalar(name string) *ast.ScalarDecl {
	if r == nil {
		return nil
	}
	return r.Resolver.LookupScalar(name)
}

func (r *projectResolver) LookupError(name string) *ast.ErrorDecl {
	if r == nil {
		return nil
	}
	return r.Resolver.LookupError(name)
}

func (r *projectResolver) LookupMiddleware(name string) *ast.MiddlewareDecl {
	if r == nil {
		return nil
	}
	return r.Resolver.LookupMiddleware(name)
}

func (r *projectResolver) Project() *semantic.Project {
	if r == nil {
		return nil
	}
	return r.Resolver.Project()
}
