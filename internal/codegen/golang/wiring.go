package golang

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// wiringData is the template input for wiring.tmpl.
type wiringData struct {
	RoutesImport     string
	SvccontextImport string
	HasRoutes        bool
	// Guards has one startup check per middleware a method runs.
	Guards []middlewareGuard
}

// middlewareGuard is one nil check in Register and the error naming the assignment to add.
type middlewareGuard struct {
	Field     string
	QuotedMsg string
}

// generateWiring writes output.wiring/wiring.go, whose Register attaches every HTTP route. It is
// written even for a design without routes, since a gen-once main.go may still call Register.
func generateWiring(proj *semantic.Project, cfg *config.Config, projectRoot string) error {
	data := wiringData{
		SvccontextImport: goImportFromRel(cfg.Package, fileDirRel(cfg.Output.Svccontext)),
		HasRoutes:        projectHasRoutes(proj),
		Guards:           middlewareGuards(proj, cfg.Output.RuntimeDisabled()),
	}
	if data.HasRoutes {
		data.RoutesImport = goImportFromRel(cfg.Package, cfg.Output.Routes)
	}
	dir := filepath.Join(projectRoot, cfg.Output.Wiring)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writeRendered(dir, "wiring.go", "wiring.tmpl", data)
}

// middlewareGuards returns a nil check for every middleware a method runs; the chain silently
// skips a nil middleware.
func middlewareGuards(proj *semantic.Project, handWired bool) []middlewareGuard {
	where := "in main.go"
	if handWired {
		where = "where you build the ServiceContext"
	}
	type applied struct {
		decl, member, ctor string
	}
	seen := map[string]applied{}
	for _, pkgName := range sortedKeys(proj.Packages) {
		pkg := proj.Packages[pkgName]
		if pkg == nil || pkgName == "" {
			continue
		}
		for _, svcName := range sortedServices(pkg) {
			svc := pkg.Services[svcName]
			if svc == nil {
				continue
			}
			for _, m := range svc.Methods {
				for _, n := range middlewareNames(m, svc.Primary) {
					if _, ok := seen[n]; !ok {
						seen[n] = applied{
							decl:   "middleware " + n,
							member: svcName + "." + m.Name,
							ctor:   "svc." + n + " = middleware.New" + n + "Middleware(/* args */)",
						}
					}
				}
			}
		}
	}
	var guards []middlewareGuard
	for _, n := range sortedKeys(seen) {
		a := seen[n]
		guards = append(guards, middlewareGuard{
			Field: "svcCtx." + n,
			QuotedMsg: strconv.Quote(fmt.Sprintf(
				"wiring: the design declares `%s` and %s runs it, but svcCtx.%s is nil - assign `%s` %s",
				a.decl, a.member, n, a.ctor, where)),
		})
	}
	return guards
}
