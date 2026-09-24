package matrix

import (
	"testing"

	collections "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/collections"
	combine "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/combine"
	numbers "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/numbers"
	scalars "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/scalars"
)

// TestGeneratedPackagesLink compiles and links the collections, combine,
// numbers and scalars types packages.
func TestGeneratedPackagesLink(t *testing.T) {
	_ = collections.Address{}
	_ = combine.PresenceMatrix{}
	_ = numbers.NumberCounter{}
	_ = scalars.Order{}
}
