package wire_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/wire"
)

// The shape a raw field is for: a message with a few fields of its own
// plus one document it carries but does not own. The document is ~2 KB,
// the size of a webhook body or a jsonb column - big enough that what a
// codec does with it dominates, small enough to be realistic.
type rawEnvelope struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	Attempt int      `json:"attempt"`
	Details wire.Raw `json:"details"`
}

type anyEnvelope struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Attempt int    `json:"attempt"`
	Details any    `json:"details"`
}

// benchDocument builds a ~2 KB JSON object, deliberately carrying the
// three values a decode into `any` cannot give back: an explicit null,
// an integer past 2^53, and a trailing zero.
func benchDocument() string {
	var b strings.Builder
	b.WriteString(`{"explicit":null,"big":12345678901234567890,"trailing":1.50,"rows":[`)
	for i := 0; i < 24; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"sku":"SKU-`)
		b.WriteString(strconv.Itoa(1000 + i))
		b.WriteString(`","qty":`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`,"note":"a line of text that makes the row realistic"}`)
	}
	b.WriteString(`]}`)
	return b.String()
}

func benchEnvelope() []byte {
	return []byte(`{"id":"evt-1","kind":"warehouse.closed","attempt":2,"details":` + benchDocument() + `}`)
}

// BenchmarkDecodeEncodeRaw is the cost of carrying the document: one
// copy on decode, and on encode the compaction encoding/json runs over
// any Marshaler's output.
func BenchmarkDecodeEncodeRaw(b *testing.B) {
	in := benchEnvelope()
	b.SetBytes(int64(len(in)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var env rawEnvelope
		if err := json.Unmarshal(in, &env); err != nil {
			b.Fatal(err)
		}
		if _, err := json.Marshal(&env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDecodeEncodeAny is the same message with the document
// declared `any`: a full parse into map[string]any on the way in and a
// full re-encode on the way out - which is also where the explicit null,
// the big integer and the trailing zero are lost.
func BenchmarkDecodeEncodeAny(b *testing.B) {
	in := benchEnvelope()
	b.SetBytes(int64(len(in)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var env anyEnvelope
		if err := json.Unmarshal(in, &env); err != nil {
			b.Fatal(err)
		}
		if _, err := json.Marshal(&env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkUnmarshalRaw isolates the decode half: the one copy
// [wire.Raw.UnmarshalJSON] makes of the bytes handed to it.
func BenchmarkUnmarshalRaw(b *testing.B) {
	doc := []byte(benchDocument())
	b.SetBytes(int64(len(doc)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var r wire.Raw
		if err := r.UnmarshalJSON(doc); err != nil {
			b.Fatal(err)
		}
	}
}

// TestUnmarshalIntoAReusedRawMakesNoAllocation pins the claim the
// benchmark rests on: decoding copies, and a Raw whose capacity already
// covers the incoming bytes copies into it rather than allocating again.
func TestUnmarshalIntoAReusedRawMakesNoAllocation(t *testing.T) {
	doc := []byte(benchDocument())
	r := make(wire.Raw, 0, len(doc))
	allocs := testing.AllocsPerRun(100, func() {
		if err := r.UnmarshalJSON(doc); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Errorf("reusing a Raw allocated %.0f time(s) per decode, want 0", allocs)
	}
}
