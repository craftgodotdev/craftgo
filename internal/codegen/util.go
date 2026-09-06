// Small ordering and string helpers shared by every emitter.
package codegen

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

// pascalCase converts a DSL package name (commonly lowercase or
// kebab-cased) into its PascalCase form for OpenAPI schema prefixing.
// Empty / single-character inputs are passed through with the first
// rune uppercased.
func pascalCase(s string) string {
	if s == "" {
		return ""
	}
	var b []byte
	upNext := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '-' || c == '_' || c == '/' {
			upNext = true
			continue
		}
		if upNext {
			if c >= 'a' && c <= 'z' {
				c -= 'a' - 'A'
			}
			upNext = false
		}
		b = append(b, c)
	}
	return string(b)
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
