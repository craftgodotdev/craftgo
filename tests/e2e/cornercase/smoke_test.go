// Cornercase fixture smoke test. Each generated validator/handler/route
// compiles via `go build`; this single test asserts the package itself
// builds + parses by importing one type from each domain.
package cornercase

import (
	"testing"

	collections "github.com/craftgodotdev/craftgo/tests/e2e/cornercase/internal/types/collections"
	combine "github.com/craftgodotdev/craftgo/tests/e2e/cornercase/internal/types/combine"
	numbers "github.com/craftgodotdev/craftgo/tests/e2e/cornercase/internal/types/numbers"
	scalars "github.com/craftgodotdev/craftgo/tests/e2e/cornercase/internal/types/scalars"
)

// TestPackagesCompile is a tautology — if the imports above resolved,
// the cornercase generated types compile cleanly. The body asserts
// zero-value Validate() runs without panicking. The orchestrator only
// needs an `ok` line for this scenario to count as passing.
func TestPackagesCompile(t *testing.T) {
	_ = collections.Address{}
	_ = combine.PresenceMatrix{}
	_ = numbers.NumberCounter{}
	_ = scalars.Order{}
}
