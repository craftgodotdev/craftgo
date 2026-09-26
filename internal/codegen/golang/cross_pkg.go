package golang

import (
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
	types := outputsOf(cfg).types
	for name := range proj.Packages {
		if name == currentPkgName {
			continue
		}
		out[name] = types.sub(name).pkg
	}
	return out
}
