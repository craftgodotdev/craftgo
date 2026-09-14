// Package docs emits the OpenAPI description of a design's HTTP surface.
// It reads the analysed project and the layer-agnostic field IR, so it
// does not depend on the language a service is generated in.
package docs

import (
	"sort"

	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// sortedKeys returns the keys of m in alphabetical order, so a document
// orders its components deterministically.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sortedPackageNames returns the project's non-blank package names in
// alphabetical order.
func sortedPackageNames(proj *semantic.Project) []string {
	out := make([]string, 0, len(proj.Packages))
	for k := range proj.Packages {
		if k != "" {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// sortedServices returns the package's service names in deterministic order.
func sortedServices(pkg *semantic.Package) []string { return sortedKeys(pkg.Services) }

// dedupeStrings drops repeat entries while preserving first-seen order.
func dedupeStrings(in []string) []string {
	if len(in) <= 1 {
		return in
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
