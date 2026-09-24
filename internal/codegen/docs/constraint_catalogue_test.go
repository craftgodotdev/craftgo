package docs

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// constraintNames returns every decorator the registry classifies as a
// constraint with a schema form.
func constraintNames() map[string]bool {
	out := map[string]bool{}
	for name, spec := range semantic.Registry {
		if spec.Constraint == 0 || spec.Constraint == semantic.ConstraintRuntime {
			continue
		}
		out[name] = true
	}
	return out
}

// schemaKeywords has a row for exactly the constraints with a schema form.
func TestSchemaKeywordsCoverConstraints(t *testing.T) {
	want := constraintNames()
	for name := range want {
		if schemaKeywords[name] == nil {
			t.Errorf("@%s is a constraint but advertises no OpenAPI keyword", name)
		}
	}
	for name := range schemaKeywords {
		if !want[name] {
			t.Errorf("schemaKeywords advertises @%s, which has no schema form in the registry", name)
		}
	}
}
