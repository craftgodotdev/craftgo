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
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// CrossPkg maps a DSL package name (the target's `package X`
// declaration) to the Go import path under
// `<modulePath>/<typesOutputDir>/<X>`. Generators look up multi-part
// DSL refs (`shared.User`) by their first segment to decide which
// Go import statement to add.
//
// A nil or empty CrossPkg indicates no cross-package context - the
// generators emit only the standard-library imports they have always
// emitted.
type CrossPkg map[string]string

// ScalarTable is the per-target-package lookup of scalar declarations
// reachable from the package being generated. Local scalars are
// keyed by bare name (`OrderID`); cross-package scalars use the
// qualified DSL form (`shared.NonEmptyID`). The codegen consults
// the table when a field's declared type is a scalar so the
// scalar's decorators (e.g. `@format(email)` on `scalar Email`)
// inherit into the field's effective validator chain.
//
// Empty / nil table disables inheritance and the generated
// validators only honour the field's own decorator list - the
// legacy single-package behaviour.
type ScalarTable map[string]*ast.ScalarDecl

// BuildScalarTable returns the lookup table for `currentPkgName`.
// Every scalar declared anywhere in the project is included once;
// scalars from other packages are keyed by their qualified DSL
// form so a field typed `shared.NonEmptyID` resolves cleanly.
//
// Returns nil when proj is nil - callers can still pass the result
// straight into [GenerateValidatorsPackage] without a guard.
func BuildScalarTable(proj *semantic.Project, currentPkgName string) ScalarTable {
	if proj == nil {
		return nil
	}
	out := ScalarTable{}
	for pkgName, p := range proj.Packages {
		if p == nil {
			continue
		}
		for sname, sd := range p.Scalars {
			if pkgName == "" || pkgName == currentPkgName {
				out[sname] = sd
				continue
			}
			out[pkgName+"."+sname] = sd
		}
	}
	return out
}

// TypeTable is the per-target-package lookup of TypeDecls reachable
// from the package being generated. Mirrors [ScalarTable] but for
// struct-shaped types: local types are keyed by bare name (`Order`),
// cross-package types by qualified form (`shared.Page`). The codegen
// consults the table to decide whether a field type carries its own
// `Validate()` method — without it, qualified refs like
// `shared.Page<T>` slipped past `pkg.Types` (local-only) and the
// recursive validate call was silently dropped.
type TypeTable map[string]*ast.TypeDecl

// BuildTypeTable returns the lookup table for `currentPkgName`.
// Every type declared anywhere in the project is included once,
// qualified for cross-package entries the same way scalars are.
func BuildTypeTable(proj *semantic.Project, currentPkgName string) TypeTable {
	if proj == nil {
		return nil
	}
	out := TypeTable{}
	for pkgName, p := range proj.Packages {
		if p == nil {
			continue
		}
		for tname, td := range p.Types {
			if pkgName == "" || pkgName == currentPkgName {
				out[tname] = td
				continue
			}
			out[pkgName+"."+tname] = td
		}
	}
	return out
}

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
//   - Single-part name (`User`) - alias is the canonical "types"
//     import; no extra import.
//   - Two-part name (`shared.User`) where the first part is in
//     [CrossPkg] - alias is the package name, extra import points
//     at the matching Go path.
//   - Two-part name with no [CrossPkg] entry - falls back to the
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
		return "types", parts[0] + genericArgsSuffix(n.Args, "types", crossPkg), extraImport{}
	}
	pkgName, sym := parts[0], parts[len(parts)-1]
	if path, ok := crossPkg[pkgName]; ok {
		return pkgName, sym + genericArgsSuffix(n.Args, "types", crossPkg), extraImport{Alias: pkgName, Path: path}
	}
	// Cross-pkg without a CrossPkg entry - fall back to dotted form.
	// The single-package legacy generator always rendered this as
	// `types.<dotted>` and emitted no extra import; preserve that so
	// existing tests/fixtures still pass.
	return "types", n.Name.String() + genericArgsSuffix(n.Args, "types", crossPkg), extraImport{}
}

// genericArgsSuffix renders the Go generic-instantiation suffix for a
// named-type ref, or "" when the ref has no type arguments. Used so
// service / handler signatures emit `Page[types.Order]` instead of
// bare `Page` — the latter trips a "cannot use generic type without
// instantiation" compile error wherever the signature lives.
//
// `localAlias` is the Go import alias the consuming file uses for
// the canonical types package (almost always "types"). Single-segment
// generic args (local types like `Order`) get prefixed with it so the
// reference resolves through the consuming file's import block.
// Cross-package args (`shared.User`) keep their DSL qualifier — the
// caller is responsible for ensuring that package is imported, same
// as for the top-level type. Multi-arg (`Pair<A, B>`) and nested
// instantiations flow through `GoTypeRef` recursively.
func genericArgsSuffix(args []*ast.TypeRef, localAlias string, crossPkg CrossPkg) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, 0, len(args))
	for _, a := range args {
		parts = append(parts, qualifyGoTypeRef(a, localAlias, crossPkg))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// qualifyGoTypeRef walks a TypeRef and qualifies its leaf named refs
// with the consuming file's local-types alias. Used to render generic
// args at the handler / service surface where local types live behind
// the `types` import. Cross-package refs (`shared.User`) keep the
// DSL-supplied qualifier; primitive types pass through unchanged.
func qualifyGoTypeRef(t *ast.TypeRef, localAlias string, crossPkg CrossPkg) string {
	if t == nil {
		return ""
	}
	if t.Map != nil {
		return "map[" + qualifyGoTypeRef(t.Map.Key, localAlias, crossPkg) + "]" + qualifyGoTypeRef(t.Map.Value, localAlias, crossPkg)
	}
	depth := t.ArrayDepth
	if depth == 0 && t.Array {
		depth = 1
	}
	leaf := ""
	if t.Named != nil {
		leaf = qualifyNamedRef(t.Named, localAlias, crossPkg)
	}
	for i := 0; i < depth; i++ {
		leaf = "[]" + leaf
	}
	if t.Optional && !isNilableGoType(leaf) {
		leaf = "*" + leaf
	}
	return leaf
}

// qualifyNamedRef applies the local-alias qualifier to a single
// NamedTypeRef. Builtins (`string`, `int`, …) and cross-package
// refs are left alone; only single-segment user-defined names get
// the alias prefix.
func qualifyNamedRef(n *ast.NamedTypeRef, localAlias string, crossPkg CrossPkg) string {
	if n == nil || n.Name == nil {
		return ""
	}
	name := n.Name.String()
	if idents.IsBuiltin(name) {
		return goNamedType(n)
	}
	parts := n.Name.Parts
	suffix := genericArgsSuffix(n.Args, localAlias, crossPkg)
	if len(parts) == 1 {
		if localAlias == "" {
			return parts[0] + suffix
		}
		return localAlias + "." + parts[0] + suffix
	}
	return name + suffix
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
