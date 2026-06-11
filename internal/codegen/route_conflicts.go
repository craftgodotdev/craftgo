// Cross-route conflict detection: reject route patterns that net/http's
// ServeMux refuses to register together, at gen time instead of at server boot.
package codegen

import (
	"fmt"
	"sort"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/route"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// routeEntryForConflict is one registered route: its HTTP verb, the final mux
// pattern, and a human label for diagnostics.
type routeEntryForConflict struct {
	verb    string
	pattern string
	label   string // "ServiceName.MethodName"
}

// ValidateRouteConflicts reports every pair of routes across the whole project
// that net/http's ServeMux (Go 1.22+) would reject at registration because they
// overlap and neither is more specific than the other - e.g.
// `GET /orders/{id}/track` vs `GET /orders/by-status/{status}`, which both match
// `/orders/by-status/track`. ServeMux panics on these at server boot; surfacing
// them here turns a runtime crash into a design-time error. Returns one message
// per conflicting pair (empty when the route set is clean).
//
// All services register on one mux (routes.RegisterAll), so the check spans
// every package; the OpenAPI basePath is a shared prefix and does not change
// which pairs conflict.
func ValidateRouteConflicts(proj *semantic.Project, cfg *config.Config) []string {
	if proj == nil {
		return nil
	}
	var routes []routeEntryForConflict
	for _, pkgName := range sortedKeys(proj.Packages) {
		p := proj.Packages[pkgName]
		if p == nil {
			continue
		}
		for _, svcName := range sortedServices(p) {
			svc := p.Services[svcName]
			if svc == nil {
				continue
			}
			for _, m := range svc.Methods {
				routes = append(routes, routeEntryForConflict{
					verb:    httpVerb(m.Verb),
					pattern: route.Resolve(cfg.OpenAPI.BasePath, svc.Primary, m),
					label:   svcName + "." + m.Name,
				})
			}
		}
	}

	var msgs []string
	for i := 0; i < len(routes); i++ {
		for j := i + 1; j < len(routes); j++ {
			a, b := routes[i], routes[j]
			if a.verb != b.verb || !route.PatternsConflict(a.pattern, b.pattern) {
				continue
			}
			msgs = append(msgs, fmt.Sprintf(
				"%s %s (%s) conflicts with %s %s (%s): both match overlapping paths and neither is more specific, so net/http's ServeMux rejects them at startup. Disambiguate one route (e.g. give the filter a distinct literal prefix or move the variable to @query).",
				a.verb, a.pattern, a.label, b.verb, b.pattern, b.label,
			))
		}
	}
	sort.Strings(msgs)
	return msgs
}
