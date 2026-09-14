package golang

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// constraintNames returns every decorator the registry classifies as a
// constraint, optionally narrowed to those with a schema form.
func constraintNames(schemaOnly bool) map[string]bool {
	out := map[string]bool{}
	for name, spec := range semantic.Registry {
		if spec.Constraint == 0 {
			continue
		}
		if schemaOnly && spec.Constraint == semantic.ConstraintRuntime {
			continue
		}
		out[name] = true
	}
	return out
}

// Every constraint decorator compiles to a runtime check. Adding one to
// the registry without a check here fails rather than silently accepting
// a decorator that enforces nothing.
func TestGoChecksCoverConstraints(t *testing.T) {
	want := constraintNames(false)
	for name := range want {
		if goChecks[name] == nil {
			t.Errorf("@%s is a constraint but renders no Go check", name)
		}
	}
	for name := range goChecks {
		if !want[name] {
			t.Errorf("goChecks renders @%s, which the registry does not classify as a constraint", name)
		}
	}
}

// The runtime-only constraints are the ones a multipart part cannot
// express as a schema keyword. Pinned so adding a row here is deliberate.
func TestRuntimeOnlyConstraints(t *testing.T) {
	want := map[string]bool{"maxSize": true, "mimeTypes": true}
	for name, spec := range semantic.Registry {
		if spec.Constraint != semantic.ConstraintRuntime {
			continue
		}
		if !want[name] {
			t.Errorf("@%s is runtime-only but is not in the pinned set", name)
		}
		delete(want, name)
	}
	for name := range want {
		t.Errorf("@%s is pinned runtime-only but the registry disagrees", name)
	}
}
