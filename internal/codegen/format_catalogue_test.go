package codegen

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/strfmt"
)

// TestFormatCatalogueMatchesShared pins the `@format` name set the validator
// emits (formatValidators) equal to the shared catalogue (strfmt.Names) that
// the analyser uses as the legal-value set. If they drift, a `@format` the
// editor accepts could reach a validator that silently emits no check
// (validate_emit_string.go skips unknown names) while OpenAPI still advertises
// the format - a field with no runtime enforcement. The test also confirms the
// analyser's enum is wired to the same leaf, so all three agree by construction.
func TestFormatCatalogueMatchesShared(t *testing.T) {
	canonical := map[string]bool{}
	for _, n := range strfmt.Names {
		canonical[n] = true
	}
	if len(canonical) == 0 {
		t.Fatal("strfmt.Names is empty")
	}
	for name := range formatValidators {
		if !canonical[name] {
			t.Errorf("formatValidators emits %q but it is not in the shared strfmt.Names catalogue", name)
		}
	}
	for name := range canonical {
		if _, ok := formatValidators[name]; !ok {
			t.Errorf("strfmt.Names lists @format(%q) but formatValidators emits no validator - the field would get NO runtime check", name)
		}
	}
	// The analyser's @format enum must be the same leaf, or the editor could
	// accept a name the shared catalogue doesn't.
	if got := len(semantic.Registry["format"].Args.Enum); got != len(strfmt.Names) {
		t.Errorf("analyser @format enum has %d names, shared catalogue has %d", got, len(strfmt.Names))
	}
}
