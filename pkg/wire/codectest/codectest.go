// Package codectest checks that a codec carries [wire.Raw] values through
// unchanged. A codec's own test calls [Run], or [RunWith] and [RunNull] with
// values in its format; the suite only round-trips values, so any format fits.
// It imports only the standard library and wire.
package codectest

import (
	"fmt"
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/wire"
)

// Codec is the codec under test; an events.Codec satisfies it.
type Codec interface {
	// Name identifies the encoding on the wire.
	Name() string
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
}

// reporter is the part of *testing.T the checks report through, so the suite's
// own tests can capture its failures.
type reporter interface {
	Helper()
	Errorf(format string, args ...any)
}

// nested puts a Raw one level down.
type nested struct {
	Note string   `json:"note"`
	Doc  wire.Raw `json:"doc"`
}

// payload holds a Raw in every position, beside plain fields that must still
// decode.
type payload struct {
	ID      string     `json:"id"`
	Attempt int        `json:"attempt"`
	Ok      bool       `json:"ok"`
	Body    wire.Raw   `json:"body"`
	Extra   wire.Raw   `json:"extra,omitempty"`
	Missing wire.Raw   `json:"missing,omitempty"`
	Nested  nested     `json:"nested"`
	List    []wire.Raw `json:"list"`
}

// JSONValues returns the JSON fixtures [Run] uses: an integer past 2^53, a
// trailing zero, a string, an object with an explicit null, and an array. The
// bare null is left to [RunNull].
func JSONValues() [][]byte {
	return [][]byte{
		[]byte(`12345678901234567890`),
		[]byte(`1.50`),
		[]byte(`"a string"`),
		[]byte(`{"explicit":null,"big":12345678901234567890,"trailing":1.50}`),
		[]byte(`["a",{"b":1},2.0]`),
	}
}

// Run checks that c carries [wire.Raw] through unchanged, using [JSONValues]
// and the JSON null. A codec whose format is not JSON calls [RunWith] and
// [RunNull] instead.
func Run(t *testing.T, c Codec) {
	t.Helper()
	RunWith(t, c, JSONValues()...)
	RunNull(t, c, []byte("null"))
}

// RunWith is [Run] over values encoded in c's own format: each must come back
// from a required, a set optional and a nested field and from array elements,
// with an unset optional left nil. No values at all is a failure.
func RunWith(t *testing.T, c Codec, values ...[]byte) {
	t.Helper()
	runWith(t, c, values...)
}

// RunNull round-trips null, the format's own null, through the same positions
// as [RunWith]; it must come back as those bytes.
func RunNull(t *testing.T, c Codec, null []byte) {
	t.Helper()
	runNull(t, c, null)
}

// runWith is [RunWith] against a [reporter].
func runWith(t reporter, c Codec, values ...[]byte) {
	t.Helper()
	if len(values) == 0 {
		t.Errorf("codectest: %s was given no values to round-trip", c.Name())
		return
	}
	for _, v := range values {
		for _, problem := range checkValue(c, v) {
			t.Errorf("codec %s, carrying %s: %s", c.Name(), v, problem)
		}
	}
}

// runNull is [RunNull] against a [reporter].
func runNull(t reporter, c Codec, null []byte) {
	t.Helper()
	for _, problem := range checkValue(c, null) {
		t.Errorf("codec %s, carrying the null %s: %s", c.Name(), null, problem)
	}
}

// checkValue round-trips value through every Raw position and returns what did
// not survive; the unset optional must come back nil.
func checkValue(c Codec, value []byte) []string {
	out, problems := roundTrip(c, value)
	if len(problems) > 0 {
		return problems
	}
	same := func(where string, got wire.Raw) {
		if string(got) != string(value) {
			problems = append(problems, fmt.Sprintf("%s came back as %s", where, got))
		}
	}
	same("a required raw field", out.Body)
	same("a set optional raw field", out.Extra)
	same("a raw field of a nested struct", out.Nested.Doc)
	if out.Missing != nil {
		problems = append(problems, fmt.Sprintf(
			"an unset optional raw field came back as %s - absence is not a value", out.Missing))
	}
	if len(out.List) != 2 {
		return append(problems, fmt.Sprintf(
			"an array of raw values came back with %d element(s), want 2", len(out.List)))
	}
	for _, got := range out.List {
		same("an element of an array of raw values", got)
	}
	return problems
}

// roundTrip encodes a payload carrying value, decodes it back and checks the
// plain fields.
func roundTrip(c Codec, value []byte) (payload, []string) {
	in := payload{
		ID:      "evt-1",
		Attempt: 3,
		Ok:      true,
		Body:    wire.Raw(value),
		Extra:   wire.Raw(value),
		Nested:  nested{Note: "one level down", Doc: wire.Raw(value)},
		List:    []wire.Raw{wire.Raw(value), wire.Raw(value)},
	}
	encoded, err := c.Marshal(&in)
	if err != nil {
		return payload{}, []string{"encoding it failed: " + err.Error()}
	}
	var out payload
	if err := c.Unmarshal(encoded, &out); err != nil {
		return payload{}, []string{"decoding what it encoded failed: " + err.Error()}
	}
	var problems []string
	if out.ID != in.ID || out.Attempt != in.Attempt || out.Ok != in.Ok {
		problems = append(problems, fmt.Sprintf(
			"the plain fields did not survive: got %q/%d/%v, want %q/%d/%v",
			out.ID, out.Attempt, out.Ok, in.ID, in.Attempt, in.Ok))
	}
	if out.Nested.Note != in.Nested.Note {
		problems = append(problems, fmt.Sprintf(
			"a plain field beside a nested raw value did not survive: got %q", out.Nested.Note))
	}
	return out, problems
}
