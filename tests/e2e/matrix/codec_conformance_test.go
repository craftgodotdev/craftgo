package matrix

import (
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
	"github.com/craftgodotdev/craftgo/pkg/wire/codectest"
)

// The codec craftgo ships is held to the contract craftgo publishes: a
// `bytes @format(raw)` value travels as the bytes of that value and
// comes back as them.
//
// The run lives here rather than beside the codec because the codec is
// in the `pkg/events` module, which imports nothing outside the standard
// library so that a generated contract package inherits nothing from it.
// This fixture is the module where the event runtime and the craftgo
// root module (where `pkg/wire` lives) are both already in scope.
func TestCodecJSONCarriesRawValues(t *testing.T) {
	codectest.Run(t, codecjson.Codec{})
}
