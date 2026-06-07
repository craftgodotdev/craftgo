// Cross-route conflict detection: reject route patterns that net/http's
// ServeMux refuses to register together, at gen time instead of at server boot.
package codegen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/config"
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
					pattern: methodFullPath(cfg.OpenAPI.BasePath, svc.Primary, m),
					label:   svcName + "." + m.Name,
				})
			}
		}
	}

	var msgs []string
	for i := 0; i < len(routes); i++ {
		for j := i + 1; j < len(routes); j++ {
			a, b := routes[i], routes[j]
			if a.verb != b.verb || !patternsConflict(a.pattern, b.pattern) {
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

// patternsConflict reports whether two same-verb mux patterns overlap with
// neither strictly more specific - the exact condition net/http rejects. It
// models craftgo's single-segment wildcards (`{name}`): patterns of different
// segment counts can never overlap, and at each shared position a literal beats
// a wildcard. The pair conflicts when one is more specific at some segment AND
// the other is more specific at another (a cross-over), or when they are the
// same pattern (every segment ties) - i.e. neither side wins outright.
func patternsConflict(a, b string) bool {
	as, bs := splitRouteSegments(a), splitRouteSegments(b)
	if len(as) != len(bs) {
		return false
	}
	aMoreSpecific, bMoreSpecific := false, false
	for i := range as {
		aWild, bWild := isWildcardSeg(as[i]), isWildcardSeg(bs[i])
		switch {
		case !aWild && !bWild:
			if as[i] != bs[i] {
				return false // disjoint at this literal segment
			}
		case !aWild && bWild:
			aMoreSpecific = true
		case aWild && !bWild:
			bMoreSpecific = true
			// both wildcard → tie, no winner at this segment
		}
	}
	// Overlapping (no disjoint segment). Conflict unless exactly one side is
	// strictly more specific overall.
	return aMoreSpecific == bMoreSpecific
}

func splitRouteSegments(pattern string) []string {
	var out []string
	for s := range strings.SplitSeq(pattern, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func isWildcardSeg(seg string) bool {
	return strings.HasPrefix(seg, "{")
}
