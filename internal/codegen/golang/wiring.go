// Wiring umbrella: the one call main.go makes to attach the design.
package golang

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// wiringData is the template input for `wiring.tmpl`.
type wiringData struct {
	RoutesImport     string
	SvccontextImport string
	HasRoutes        bool
	// Guards is one startup check per HTTP middleware the design APPLIES.
	// A declared-but-unapplied middleware is a dead wire the design layer
	// already reports, so guarding it here would complain twice about one
	// mistake.
	Guards []middlewareGuard
}

// middlewareGuard is one nil check in Register: the field main.go has to
// assign, and the message naming the line that assigns it.
type middlewareGuard struct {
	Field     string
	QuotedMsg string
}

// generateWiring writes the wiring package: one `Register` call attaching
// every HTTP route the design declares.
//
// It is emitted for every project, including one declaring none, because
// main.go is written once and calls it unconditionally.
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

// middlewareGuards collects a startup check for every middleware a method
// actually runs.
//
// The suggested line carries `/* args */` rather than `()`: the impl is a
// gen-once scaffold whose parameters are the author's to change, so
// naming a signature would be a guess. This is the spelling
// middleware.tmpl's own wiring example uses.
//
// A nil entry is skipped by the chain rather than called, so without the
// check a middleware the design states and main.go forgets is simply
// absent at runtime: no error, no log line, and the guarantee the design
// makes is not kept.
func middlewareGuards(proj *semantic.Project, handWired bool) []middlewareGuard {
	// A `main: "-"` project has no generated main.go to point at: its
	// container is built by hand, and that is where the assignment goes.
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
