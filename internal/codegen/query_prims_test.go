package codegen

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/idents"
)

// TestQueryPrimsMatchWireParseable pins the codegen metadata table
// (queryPrims, which carries each primitive's strconv parser / Go type /
// label) to the shared wire-parseable set the analyser rejects against
// (idents.IsWireParseable). The membership question - "can this primitive be
// parsed from a single wire string?" - is decided once in idents; queryPrims
// only adds the per-primitive rendering. If a primitive is added to one but
// not the other, the editor and the generator would disagree on what binds.
func TestQueryPrimsMatchWireParseable(t *testing.T) {
	for name := range queryPrims {
		if !idents.IsWireParseable(name) {
			t.Errorf("queryPrims has %q but idents.IsWireParseable rejects it", name)
		}
	}
	for name := range idents.BuiltinTypes {
		if idents.IsWireParseable(name) {
			if _, ok := queryPrims[name]; !ok {
				t.Errorf("idents.IsWireParseable accepts %q but queryPrims has no binder metadata for it", name)
			}
		} else {
			if _, ok := queryPrims[name]; ok {
				t.Errorf("queryPrims carries %q but idents.IsWireParseable rejects it as non-wire-parseable", name)
			}
		}
	}
}
