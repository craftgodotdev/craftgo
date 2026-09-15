// Small ordering and string helpers shared by every emitter.
package golang

import (
	"sort"

	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// sortedKeys returns the keys of m in alphabetical order. Used by
// the merge to produce deterministic schema ordering.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sortedServices returns the package's service names in deterministic order.
func sortedServices(pkg *semantic.Package) []string { return sortedKeys(pkg.Services) }

// dedupeStrings drops repeat entries from a name list while preserving
// first-seen order. Used by cross-field codegen so a typo'd duplicate
// (`@requiresOneOf(a, a, b)`) doesn't produce `v.A == nil && v.A == nil`
// which `go vet` flags as a redundant boolean.
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

// sortedPackageNames returns the project's non-blank package names in
// alphabetical order so every per-package phase emits in a stable order.
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
