package wire_test

import (
	"encoding/json"
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/wire"
)

// A document with an explicit null, an integer past 2^53 and 1.50 survives a
// round trip byte for byte.
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

// A nil Raw encodes as JSON null.
func TestNilRawEncodesAsNull(t *testing.T) {
	out, err := json.Marshal(wire.Raw(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "null" {
		t.Errorf("nil Raw encoded as %s, want null", out)
	}
}

// A literal null decodes to the four bytes `null` and re-encodes as null.
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

// UnmarshalJSON does not alias the caller's buffer.
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

// An explicit null leaves a *Raw nil but decodes into a Raw as the bytes `null`.
func TestPointerToRawLosesTheNullTheValueKeeps(t *testing.T) {
	var got struct {
		Ptr   *wire.Raw `json:"ptr"`
		Value wire.Raw  `json:"value"`
	}
	if err := json.Unmarshal([]byte(`{"ptr":null,"value":null}`), &got); err != nil {
		t.Fatal(err)
	}
	if got.Ptr != nil {
		t.Errorf("*Raw held %s; the shape exists because it holds nil here", *got.Ptr)
	}
	if string(got.Value) != "null" {
		t.Errorf("Raw decoded an explicit null to %q, want the four bytes `null`", got.Value)
	}
}
