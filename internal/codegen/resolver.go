package codegen

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
// qualified (`shared.Order`) — matching the keying contract of
// [ScalarTable] / [TypeTable] / [EnumTable] which the resolver
// composes from.
//
// nil-tolerant: every method returns the zero result when the
// receiver is nil, so single-package fixtures and callers without
// project context work unchanged — mirroring the `nil`
// ScalarTable / TypeTable / EnumTable handling elsewhere.

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// ErrorTable is the per-target-package lookup of ErrorDecls
// reachable from the package being generated. Mirrors [TypeTable]:
// local errors keyed bare (`NotFoundErr`), cross-package errors by
// qualified DSL form (`shared.NotFoundErr`). The OpenAPI emitter
// needs this to resolve `@errors(shared.NotFoundErr)` references
// against the right per-error response schema and to register the
// error body component.
type ErrorTable map[string]*ast.ErrorDecl

// BuildErrorTable returns the lookup table for `currentPkgName`.
// Mirrors [BuildTypeTable] / [BuildEnumTable].
func BuildErrorTable(proj *semantic.Project, currentPkgName string) ErrorTable {
	if proj == nil {
		return nil
	}
	out := ErrorTable{}
	for pkgName, p := range proj.Packages {
		if p == nil {
			continue
		}
		for ename, ed := range p.Errors {
			if pkgName == "" || pkgName == currentPkgName {
				out[ename] = ed
				continue
			}
			out[pkgName+"."+ename] = ed
		}
	}
	return out
}

// MiddlewareTable is the per-target-package lookup of MiddlewareDecls
// reachable from the package being generated. Same keying contract as
// the other tables, included for symmetry with the codegen-side
// lookups so a cross-pkg middleware rule has the same plumbed lookup
// as everything else.
type MiddlewareTable map[string]*ast.MiddlewareDecl

// BuildMiddlewareTable returns the lookup table for `currentPkgName`.
func BuildMiddlewareTable(proj *semantic.Project, currentPkgName string) MiddlewareTable {
	if proj == nil {
		return nil
	}
	out := MiddlewareTable{}
	for pkgName, p := range proj.Packages {
		if p == nil {
			continue
		}
		for mname, md := range p.Middlewares {
			if pkgName == "" || pkgName == currentPkgName {
				out[mname] = md
				continue
			}
			out[pkgName+"."+mname] = md
		}
	}
	return out
}

// ProjectResolver bundles every per-package-target lookup table the
// codegen layer needs to resolve qualified cross-package references.
// One resolver per generated package; built by
// [BuildProjectResolver] from a [semantic.Project] + [config.Config].
//
// Pass it as a single parameter instead of plumbing 4-5 separate
// tables. Lookup methods are nil-tolerant — `(*ProjectResolver)(nil)`
// is a usable zero value that always misses, matching the legacy
// behaviour every callsite already handles for `nil` maps.
type ProjectResolver struct {
	Types       TypeTable
	Enums       EnumTable
	Scalars     ScalarTable
	Errors      ErrorTable
	Middlewares MiddlewareTable
	CrossPkg    CrossPkg
}

// BuildProjectResolver assembles every table the codegen layer needs
// for `currentPkgName`. Returns a non-nil resolver with empty tables
// when proj is nil so callers don't have to nil-check before use.
func BuildProjectResolver(proj *semantic.Project, cfg *config.Config, currentPkgName string) *ProjectResolver {
	return &ProjectResolver{
		Types:       BuildTypeTable(proj, currentPkgName),
		Enums:       BuildEnumTable(proj, currentPkgName),
		Scalars:     BuildScalarTable(proj, currentPkgName),
		Errors:      BuildErrorTable(proj, currentPkgName),
		Middlewares: BuildMiddlewareTable(proj, currentPkgName),
		CrossPkg:    BuildCrossPkg(proj, cfg, currentPkgName),
	}
}

// LookupType returns the type decl bound to name (bare for local,
// qualified `pkg.Name` for cross-package), or nil when no match.
func (r *ProjectResolver) LookupType(name string) *ast.TypeDecl {
	if r == nil {
		return nil
	}
	return r.Types[name]
}

// LookupEnum is the enum counterpart of [LookupType].
func (r *ProjectResolver) LookupEnum(name string) *ast.EnumDecl {
	if r == nil {
		return nil
	}
	return r.Enums[name]
}

// LookupScalar is the scalar counterpart of [LookupType].
func (r *ProjectResolver) LookupScalar(name string) *ast.ScalarDecl {
	if r == nil {
		return nil
	}
	return r.Scalars[name]
}

// LookupError is the error counterpart of [LookupType].
func (r *ProjectResolver) LookupError(name string) *ast.ErrorDecl {
	if r == nil {
		return nil
	}
	return r.Errors[name]
}

// LookupMiddleware is the middleware counterpart of [LookupType].
func (r *ProjectResolver) LookupMiddleware(name string) *ast.MiddlewareDecl {
	if r == nil {
		return nil
	}
	return r.Middlewares[name]
}

// crossPkgMap returns the underlying [CrossPkg] alias→import map for
// emitters that still consume the bare map (transport's
// resolveTypeRef, collectRequestFieldImports, …). nil receiver
// yields nil — those emitters treat nil as "no cross-package
// imports needed".
func (r *ProjectResolver) crossPkgMap() CrossPkg {
	if r == nil {
		return nil
	}
	return r.CrossPkg
}

// ImportPath returns the Go import path for the DSL package alias,
// or "" when the alias isn't in the cross-package map. Used by emit
// sites that need to register an import when they output a qualified
// Go identifier like `shared.ColorRed`.
func (r *ProjectResolver) ImportPath(pkgAlias string) string {
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
func (r *ProjectResolver) QualifierFor(n *ast.NamedTypeRef) (string, string) {
	if r == nil || n == nil || n.Name == nil {
		return "", ""
	}
	parts := n.Name.Parts
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0] + ".", r.ImportPath(parts[0])
}
