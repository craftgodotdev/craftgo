package golang

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
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

// importsInto returns a named-ref visitor that adds to set the import of each
// package-qualified ref it is called on.
func (c crossPkg) importsInto(set map[string]bool) func(*ast.NamedTypeRef) {
	return func(n *ast.NamedTypeRef) {
		if imp := crossPkgImportFor(n, c); imp != "" {
			set[imp] = true
		}
	}
}
