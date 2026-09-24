package golang

import (
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// projectResolver resolves bare and package-qualified names for one generated
// package and maps the other packages to Go import paths.
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

// resolverFor returns r, or a resolver over pkg alone when the caller has
// no project context.
func resolverFor(pkg *semantic.Package, r *projectResolver) *projectResolver {
	if r != nil {
		return r
	}
	return &projectResolver{Resolver: semantic.PackageResolver(pkg)}
}
