package codegen

// Cross-package codegen context. When the project has more than one
// DSL package (files declaring different `package X` keywords),
// generators need a way to translate a multi-part type reference
// like `shared.User` into a Go import path
// (`<module>/internal/types/shared`).
//
// [CrossPkg] is the lookup table: package-name → Go-import-path. It
// is built by [BuildCrossPkg] from the [semantic.Project] result and
// passed to the multi-package generator entry points; passing nil
// preserves the legacy single-package behaviour.

import (
	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/config"
	"github.com/dropship-dev/craftgo/internal/semantic"
)

// CrossPkg maps a DSL package name (the target's `package X`
// declaration) to the Go import path under
// `<modulePath>/<typesOutputDir>/<X>`. Generators look up multi-part
// DSL refs (`shared.User`) by their first segment to decide which
// Go import statement to add.
//
// A nil or empty CrossPkg indicates no cross-package context — the
// generators emit only the standard-library imports they have always
// emitted.
type CrossPkg map[string]string

// BuildCrossPkg returns a fully-populated lookup table for every
// non-current package in the project. The current package is
// excluded so a self-reference (`design.Foo` inside `package design`)
// renders as bare `Foo` without dragging in a redundant Go import.
//
// Caller passes `currentPkgName` = the package being generated;
// passing "" returns the entire project mapping (useful for tools
// that don't know the destination yet).
func BuildCrossPkg(proj *semantic.Project, cfg *config.Config, currentPkgName string) CrossPkg {
	if proj == nil || cfg == nil {
		return nil
	}
	out := CrossPkg{}
	typesPathPrefix := goImportFromRel(cfg.Package, cfg.Output.Types)
	for name := range proj.Packages {
		if name == "" || name == currentPkgName {
			continue
		}
		out[name] = typesPathPrefix + "/" + name
	}
	return out
}

// crossPkgImportFor returns the Go import path for a multi-part DSL
// type reference, or "" when the prefix isn't in the cross-pkg map.
// Used by [collectImports] to grow the file's import block when a
// field type or generic argument crosses a package boundary.
func crossPkgImportFor(n *ast.NamedTypeRef, crossPkg CrossPkg) string {
	if n == nil || n.Name == nil || len(crossPkg) == 0 {
		return ""
	}
	if len(n.Name.Parts) < 2 {
		return ""
	}
	return crossPkg[n.Name.Parts[0]]
}

// resolveTypeRef classifies a NamedTypeRef for handler / logic
// rendering. Returns the Go-side alias, the bare type name, and an
// optional extra import row.
//
// Three shapes are handled:
//
//   - Single-part name (`User`) — alias is the canonical "types"
//     import; no extra import.
//   - Two-part name (`shared.User`) where the first part is in
//     [CrossPkg] — alias is the package name, extra import points
//     at the matching Go path.
//   - Two-part name with no [CrossPkg] entry — falls back to the
//     dotted form via "types"; correctness then depends on the
//     project's `<module>/internal/types/<pkg>` directory existing,
//     matching the legacy behaviour for cross-package refs in
//     single-package codegen.
func resolveTypeRef(n *ast.NamedTypeRef, crossPkg CrossPkg) (alias, bare string, extra extraImport) {
	if n == nil || n.Name == nil {
		return "types", "", extraImport{}
	}
	parts := n.Name.Parts
	if len(parts) == 1 {
		return "types", parts[0], extraImport{}
	}
	pkgName, sym := parts[0], parts[len(parts)-1]
	if path, ok := crossPkg[pkgName]; ok {
		return pkgName, sym, extraImport{Alias: pkgName, Path: path}
	}
	// Cross-pkg without a CrossPkg entry — fall back to dotted form.
	// The single-package legacy generator always rendered this as
	// `types.<dotted>` and emitted no extra import; preserve that so
	// existing tests/fixtures still pass.
	return "types", n.Name.String(), extraImport{}
}

// walkCrossPkgImports recurses into a TypeRef and grows set with
// every Go import path implied by a multi-part reference. Map
// keys/values and generic args are visited so a `map<string,
// shared.User>` adds the `shared` import.
func walkCrossPkgImports(t *ast.TypeRef, crossPkg CrossPkg, set map[string]bool) {
	if t == nil || len(crossPkg) == 0 {
		return
	}
	if t.Map != nil {
		walkCrossPkgImports(t.Map.Key, crossPkg, set)
		walkCrossPkgImports(t.Map.Value, crossPkg, set)
		return
	}
	if t.Named == nil {
		return
	}
	if imp := crossPkgImportFor(t.Named, crossPkg); imp != "" {
		set[imp] = true
	}
	for _, a := range t.Named.Args {
		walkCrossPkgImports(a, crossPkg, set)
	}
}

