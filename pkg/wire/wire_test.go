package wire_test

import (
	"encoding/json"
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/wire"
)

// A document survives a round trip byte for byte. Each of the three
// values is a loss a decode into `any` would take: an explicit null
// collapses to Go nil, an integer past 2^53 comes back as a float64
// with different digits, and 1.50 re-encodes as 1.5.
func TestRawRoundTripsByteForByte(t *testing.T) {
	const doc = `{"explicit":null,"big":12345678901234567890,"trailing":1.50}`

	type holder struct {
		Payload wire.Raw `json:"payload"`
	}
	in := `{"payload":` + doc + `}`

	var got holder
	if err := json.Unmarshal([]byte(in), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(got.Payload) != doc {
		t.Fatalf("decoded to %s, want the bytes that arrived: %s", got.Payload, doc)
	}
	out, err := json.Marshal(&got)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if string(out) != in {
		t.Fatalf("re-encoded to %s, want %s", out, in)
	}
}

// A nil Raw is the absent value and encodes as JSON null - the one
// thing the type writes that it was not given.
func TestNilRawEncodesAsNull(t *testing.T) {
	out, err := json.Marshal(wire.Raw(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "null" {
		t.Errorf("nil Raw encoded as %s, want null", out)
	}
}

// A literal `null` that arrived is kept as the four bytes it is: it is
// a JSON value, not the absence of one, and only the field's own
// pointer says whether the key was there at all.
func TestLiteralNullIsKept(t *testing.T) {
	var r wire.Raw
	if err := json.Unmarshal([]byte("null"), &r); err != nil {
		t.Fatal(err)
	}
	if string(r) != "null" {
		t.Fatalf("a literal null decoded to %q, want the four bytes `null`", r)
	}
	out, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "null" {
		t.Errorf("re-encoded to %s, want null", out)
	}
}

// Unmarshal copies rather than aliasing the decoder's buffer, which the
// caller is free to reuse the moment the call returns.
func TestUnmarshalCopiesTheBytes(t *testing.T) {
	buf := []byte(`{"a":1}`)
	var r wire.Raw
	if err := r.UnmarshalJSON(buf); err != nil {
		t.Fatal(err)
	}
	buf[2] = 'b'
	if string(r) != `{"a":1}` {
		t.Errorf("Raw aliased the caller's buffer: %s", r)
	}
}
