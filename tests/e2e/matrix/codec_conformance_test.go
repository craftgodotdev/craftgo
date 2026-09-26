package matrix

import (
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
	"github.com/craftgodotdev/craftgo/pkg/wire/codectest"
)

// codecjson carries a `bytes @format(raw)` value as its own bytes, both ways.
// It runs here because the pkg/events module requires no other module.
func TestCodecJSONCarriesRawValues(t *testing.T) {
	codectest.Run(t, codecjson.Codec{})
}
