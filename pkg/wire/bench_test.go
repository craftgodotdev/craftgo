package wire_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/wire"
)

// rawEnvelope is a message carrying a ~2 KB document as a Raw.
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

// benchDocument builds a ~2 KB JSON object with an explicit null, an integer
// past 2^53 and a trailing zero.
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

// BenchmarkDecodeEncodeRaw decodes and re-encodes a message carrying the
// document as a Raw.
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

// BenchmarkDecodeEncodeAny is BenchmarkDecodeEncodeRaw with the document
// declared `any`.
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

// BenchmarkUnmarshalRaw measures [wire.Raw.UnmarshalJSON] alone.
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

// Decoding into a Raw whose capacity covers the input does not allocate.
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
