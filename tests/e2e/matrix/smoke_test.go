// Compile-and-link smoke check for the cornercase fixtures. Referencing one
// type from each generated domain forces the whole tree of generated
// validators / handlers / routes to compile and link; if any of them failed
// to build, this test would not.
package matrix

import (
	"testing"

	collections "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/collections"
	combine "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/combine"
	numbers "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/numbers"
	scalars "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/scalars"
)

// TestGeneratedPackagesLink references a type from each domain so a build
// failure anywhere in the generated tree surfaces as a failed test.
func TestGeneratedPackagesLink(t *testing.T) {
	_ = collections.Address{}
	_ = combine.PresenceMatrix{}
	_ = numbers.NumberCounter{}
	_ = scalars.Order{}
}
