package golang

// Project-wide symbol resolver for the codegen layer.
//
// Codegen runs per-DSL-package, but a field can reference a symbol
// from a sibling package via `import "shared"` + `shared.Foo`. The
// local-only lookup `pkg.Types[name]` misses the qualified
// `"shared.Foo"` key and the reference is dropped.
//
// [ProjectResolver] bundles every per-package lookup table a
// generator needs into one struct, so each site calls
// `r.LookupEnum(name)` instead of `pkg.Enums[name]` and the caller
// plumbing is one parameter instead of four.
//
// Local entries are keyed bare (`Order`), cross-package entries
// qualified (`shared.Order`) - matching the keying contract of
// [ScalarTable] / [TypeTable] / [EnumTable] which the resolver
// composes from.
//
// Every generator entry point normalises a nil resolver through
// [resolverFor], so lookups never fall back to a package's local tables;
// the methods stay nil-tolerant for the few callers with no context at all.

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// projectResolver bundles every per-package-target lookup table the
// codegen layer needs to resolve qualified cross-package references.
// One resolver per generated package; built by
// [buildProjectResolver] from a [semantic.Project] + [config.Config].
//
// Pass it as a single parameter instead of plumbing 4-5 separate
// tables. Lookup methods are nil-tolerant - `(*projectResolver)(nil)`
// is a usable zero value that always misses, matching the legacy
// behaviour every callsite already handles for `nil` maps.
type projectResolver struct {
	*semantic.Resolver
	CrossPkg crossPkg
}

// buildProjectResolver assembles every table the codegen layer needs
// for `currentPkgName`. Returns a non-nil resolver with empty tables
// when proj is nil so callers don't have to nil-check before use.
func buildProjectResolver(proj *semantic.Project, cfg *config.Config, currentPkgName string) *projectResolver {
	return &projectResolver{
		Resolver: semantic.NewResolver(proj, currentPkgName),
		CrossPkg: buildCrossPkg(proj, cfg, currentPkgName),
	}
}

// ImportPath returns the Go import path for the DSL package alias,
// or "" when the alias isn't in the cross-package map. Used by emit
// sites that need to register an import when they output a qualified
// Go identifier like `shared.ColorRed`.
func (r *projectResolver) ImportPath(pkgAlias string) string {
	if r == nil || r.CrossPkg == nil {
		return ""
	}
	return r.CrossPkg[pkgAlias]
}

// QualifierFor inspects a named ref and returns (goPrefix, importPath):
//   - goPrefix is the Go package qualifier WITH trailing dot
//     (e.g. `"shared."`) for cross-pkg refs, empty for local
//   - importPath is the Go import path to register on the generated
//     file when the prefix is non-empty
//
// Returns ("", "") for nil receiver / nil ref / bare ref so callers
// can use the result unconditionally:
//
//	prefix, path := r.QualifierFor(n)
//	if path != "" { uses[path] = true }
//	emit(prefix + ed.Name + ...)
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

// The Lookup* forwarders keep `(*projectResolver)(nil)` usable: emit sites
// read the result without a guard, and a promoted method would dereference
// the nil outer pointer to reach the embedded resolver.

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
