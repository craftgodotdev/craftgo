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
	TransportImport  string
	SvccontextImport string
	HasRoutes        bool
	HasConsumers     bool
	// HasEvents gates the nil-bus guard: publishing needs the bus as much
	// as consuming does.
	HasEvents bool
	// EventSummary names what the design declares, for the nil-bus error.
	EventSummary string
	// Guards is one startup check per HTTP middleware the design APPLIES.
	// The consume half is guarded in SubscribeAll instead - see
	// [middlewareGuards]. A declared-but-unapplied middleware is a dead
	// wire the design layer already reports, so guarding it here would
	// complain twice about one mistake.
	Guards []middlewareGuard
}

// middlewareGuard is one nil check in Register: the field main.go has to
// assign, and the message naming the line that assigns it.
type middlewareGuard struct {
	Field     string
	QuotedMsg string
}

// generateWiring writes the wiring package: one `Register` call attaching
// every HTTP route and event consumer the design declares.
//
// It is emitted for every project, including one declaring neither, because
// main.go is written once and calls it unconditionally.
func generateWiring(proj *semantic.Project, cfg *config.Config, projectRoot string) error {
	consumers, published := 0, 0
	hasEvents := eventsEnabled(proj, cfg)
	if hasEvents {
		consumers = len(proj.Consumers())
		published = len(proj.Events())
	}
	httpGuards, _ := middlewareGuards(proj, cfg.Output.RuntimeDisabled())
	data := wiringData{
		SvccontextImport: goImportFromRel(cfg.Package, fileDirRel(cfg.Output.Svccontext)),
		HasRoutes:        projectHasRoutes(proj),
		HasConsumers:     consumers > 0,
		HasEvents:        hasEvents,
		EventSummary:     eventSummary(published, consumers),
		Guards:           httpGuards,
	}
	if data.HasRoutes {
		data.RoutesImport = goImportFromRel(cfg.Package, cfg.Output.Routes)
	}
	if data.HasConsumers {
		data.TransportImport = goImportFromRel(cfg.Package, cfg.Output.Transport)
	}
	dir := filepath.Join(projectRoot, cfg.Output.Wiring)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writeRendered(dir, "wiring.go", "wiring.tmpl", data)
}

// middlewareGuards collects a startup check for every middleware a member
// actually runs. One pass builds both kinds, so the rule - a middleware
// the design applies must be wired - is decided once; they come back
// apart because they are CHECKED in different places.
//
// The HTTP half goes in wiring.Register, which is the HTTP path. The
// consume half goes in SubscribeAll, because Register is not the call
// every consumer makes: a consumer deployable owns no *server.Server and
// several skip Register entirely, calling SubscribeAll directly. Register
// calls SubscribeAll itself, so the consume half is still checked on that
// path too - once, not twice.
//
// The suggested line carries `/* args */` rather than `()`: the impl is a
// gen-once scaffold whose parameters are the author's to change, so
// naming a signature would be a guess. This is the spelling
// middleware.tmpl's own wiring example uses.
//
// A nil entry is skipped by the chain rather than called
// ([events.Chain] and [server.Chain] both do this), so without the check
// a middleware the design states and main.go forgets is simply absent at
// runtime: no error, no log line, and the guarantee the design makes is
// not kept.
func middlewareGuards(proj *semantic.Project, handWired bool) (http, consume []middlewareGuard) {
	// A `main: "-"` project has no generated main.go to point at: its
	// container is built by hand, and that is where the assignment goes.
	where := "in main.go"
	if handWired {
		where = "where you build the ServiceContext"
	}
	type applied struct {
		field, decl, member string
		ctor                string
		isConsume           bool
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
							field:  "svcCtx." + n,
							decl:   "middleware " + n,
							member: svcName + "." + m.Name,
							ctor:   "svc." + n + " = middleware.New" + n + "Middleware(/* args */)",
						}
					}
				}
			}
			for _, cd := range svc.Consumers {
				for _, n := range consumeMiddlewareNames(cd, svc.Primary) {
					if _, ok := seen[n]; !ok {
						seen[n] = applied{
							field:     "svcCtx.Events.Consume." + n,
							decl:      "consume middleware " + n,
							member:    svcName + "." + cd.Name,
							ctor:      "svc.Events.Consume." + n + " = consume.New" + n + "Middleware(/* args */)",
							isConsume: true,
						}
					}
				}
			}
		}
	}
	for _, n := range sortedKeys(seen) {
		a := seen[n]
		g := middlewareGuard{
			Field: a.field,
			QuotedMsg: strconv.Quote(fmt.Sprintf(
				"%s: the design declares `%s` and %s runs it, but %s is nil - assign `%s` %s",
				guardPrefix(a.isConsume), a.decl, a.member, a.field, a.ctor, where)),
		}
		if a.isConsume {
			consume = append(consume, g)
			continue
		}
		http = append(http, g)
	}
	return http, consume
}

// guardPrefix names the call that refused, so the message reads as the
// one the caller made.
func guardPrefix(isConsume bool) string {
	if isConsume {
		return "subscribe"
	}
	return "wiring"
}

// eventSummary phrases what the design declares for the nil-bus error, so
// the reader is told which half of the wiring they are missing.
func eventSummary(published, consumers int) string {
	switch {
	case consumers == 0:
		return fmt.Sprintf("%d event(s)", published)
	case published == 0:
		return fmt.Sprintf("%d consumer(s)", consumers)
	}
	return fmt.Sprintf("%d event(s) and %d consumer(s)", published, consumers)
}
