package golang

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// crossPkg maps a DSL package name to its Go import path, `<module>/<types dir>/<name>`.
type crossPkg map[string]string

// buildCrossPkg maps every package but currentPkgName, whose own names stay
// unqualified; "" maps them all.
func buildCrossPkg(proj *semantic.Project, cfg *config.Config, currentPkgName string) crossPkg {
	if proj == nil || cfg == nil {
		return nil
	}
	out := crossPkg{}
	typesPathPrefix := typesImportRoot(cfg)
	for name := range proj.Packages {
		if name == "" || name == currentPkgName {
			continue
		}
		out[name] = typesPathPrefix + "/" + name
	}
	return out
}

// crossPkgImportFor returns the import path of a package-qualified ref, or ""
// for a bare or unknown one.
func crossPkgImportFor(n *ast.NamedTypeRef, table crossPkg) string {
	if n == nil || n.Name == nil || len(table) == 0 {
		return ""
	}
	if len(n.Name.Parts) < 2 {
		return ""
	}
	return table[n.Name.Parts[0]]
}

// resolveTypeRef returns the Go package alias, the name and any extra import a
// handler uses to reference n, and what the reference reaches.
func resolveTypeRef(n *ast.NamedTypeRef, crossPkg crossPkg) (alias, bare string, extra extraImport, use typeRefUse) {
	if n == nil || n.Name == nil {
		return "types", "", extraImport{}, use
	}
	parts := n.Name.Parts
	if len(parts) == 1 {
		use.LocalTypes = true
		return "types", parts[0] + genericArgsSuffix(n.Args, "types", crossPkg, &use), extraImport{}, use
	}
	pkgName, sym := parts[0], parts[len(parts)-1]
	if path, ok := crossPkg[pkgName]; ok {
		return pkgName, sym + genericArgsSuffix(n.Args, "types", crossPkg, &use), extraImport{Alias: pkgName, Path: path}, use
	}
	// A package missing from crossPkg stays dotted under `types`, with no import.
	use.LocalTypes = true
	return "types", n.Name.String() + genericArgsSuffix(n.Args, "types", crossPkg, &use), extraImport{}, use
}

// typeRefUse records what a rendered type reference reaches.
type typeRefUse struct {
	// LocalTypes reports whether the rendered text names the local types
	// package, directly or in a generic argument (`shared.Page<Order>`).
	LocalTypes bool
}

// genericArgsSuffix renders the instantiation `[A, B]` of args, qualifying local
// types with localAlias; "" for no args.
func genericArgsSuffix(args []*ast.TypeRef, localAlias string, crossPkg crossPkg, use *typeRefUse) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, 0, len(args))
	for _, a := range args {
		parts = append(parts, qualifyGoTypeRef(a, localAlias, crossPkg, use))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// qualifyGoTypeRef renders t with its local named types qualified by localAlias.
func qualifyGoTypeRef(t *ast.TypeRef, localAlias string, crossPkg crossPkg, use *typeRefUse) string {
	if t == nil {
		return ""
	}
	if t.Map != nil {
		return "map[" + qualifyGoTypeRef(t.Map.Key, localAlias, crossPkg, use) + "]" + qualifyGoTypeRef(t.Map.Value, localAlias, crossPkg, use)
	}
	depth := t.ArrayDepth
	if depth == 0 && t.Array {
		depth = 1
	}
	leaf := ""
	if t.Named != nil {
		leaf = qualifyNamedRef(t.Named, localAlias, crossPkg, use)
	}
	for i := 0; i < depth; i++ {
		leaf = "[]" + leaf
	}
	if t.Optional && !isNilableGoType(leaf) {
		leaf = "*" + leaf
	}
	return leaf
}

// qualifyNamedRef prefixes a bare user-defined name with localAlias; builtins
// and package-qualified names stay as written.
func qualifyNamedRef(n *ast.NamedTypeRef, localAlias string, crossPkg crossPkg, use *typeRefUse) string {
	if n == nil || n.Name == nil {
		return ""
	}
	name := n.Name.String()
	if prims.Is(name) {
		return goNamedType(n)
	}
	parts := n.Name.Parts
	suffix := genericArgsSuffix(n.Args, localAlias, crossPkg, use)
	if len(parts) == 1 {
		if localAlias == "" {
			return parts[0] + suffix
		}
		if use != nil {
			use.LocalTypes = true
		}
		return localAlias + "." + parts[0] + suffix
	}
	return name + suffix
}

// walkCrossPkgImports adds to set the import of every package-qualified ref in
// t, through maps and generic arguments.
func walkCrossPkgImports(t *ast.TypeRef, crossPkg crossPkg, set map[string]bool) {
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
