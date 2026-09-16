// Package codectest is the conformance suite for the one promise every
// codec owes [wire.Raw]: a value declared `bytes @format(raw)` in a
// design travels as the bytes of that value in the codec's own format,
// and comes back byte for byte.
//
// A codec implementation runs [Run] from its own test and is done. The
// suite never looks at the encoded bytes - it round-trips values through
// the codec and compares what comes out, so it holds a JSON codec, a
// msgpack codec and a CBOR codec to the same standard without knowing
// which it is testing.
//
// It imports nothing but the standard library and [wire]: a codec lives
// wherever its dependencies do, and this must not drag them - or the
// craftgo toolchain - into anyone's module graph.
package codectest

import (
	"fmt"
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/wire"
)

// Codec is the three methods an event codec implements. It is declared
// here structurally rather than imported so this package stays free of
// the event runtime: any `events.Codec` satisfies it as it stands, and
// so does a codec that never heard of craftgo's event bus.
type Codec interface {
	// Name identifies the encoding on the wire.
	Name() string
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
}

// reporter is the part of *testing.T the checks report through. The
// checks themselves return their findings, so the suite can be held to
// its own standard: a test reads the findings back instead of taking a
// failure it cannot inspect.
type reporter interface {
	Helper()
	Errorf(format string, args ...any)
}

// nested is a struct reached through a field, so the suite covers a Raw
// the codec meets one level down rather than only at the top.
type nested struct {
	Note string   `json:"note"`
	Doc  wire.Raw `json:"doc"`
}

// payload carries every shape a design can put a raw value in, next to
// the plain fields whose decoding must not be disturbed by them.
type payload struct {
	ID      string     `json:"id"`
	Attempt int        `json:"attempt"`
	Ok      bool       `json:"ok"`
	Body    wire.Raw   `json:"body"`
	Extra   *wire.Raw  `json:"extra"`
	Missing *wire.Raw  `json:"missing"`
	Nested  nested     `json:"nested"`
	List    []wire.Raw `json:"list"`
}

// JSONValues returns the default fixtures: values that are legal JSON
// and that a decode into `any` cannot give back unchanged - an integer
// past 2^53, a trailing zero, and a document holding both next to an
// explicit null - plus an array, whose elements must survive too. The
// bare `null` is not among them; [RunNull] has it, and says why.
func JSONValues() [][]byte {
	return [][]byte{
		[]byte(`12345678901234567890`),
		[]byte(`1.50`),
		[]byte(`"a string"`),
		[]byte(`{"explicit":null,"big":12345678901234567890,"trailing":1.50}`),
		[]byte(`["a",{"b":1},2.0]`),
	}
}

// Run asserts that c carries [wire.Raw] through unchanged, using the
// JSON fixtures from [JSONValues] plus the JSON null. A codec whose
// format is not JSON cannot embed those bytes, and calls [RunWith] and
// [RunNull] with the equivalent values in its own encoding instead.
func Run(t *testing.T, c Codec) {
	t.Helper()
	RunWith(t, c, JSONValues()...)
	RunNull(t, c, []byte("null"))
}

// RunWith is [Run] over values already encoded in c's own format. Each
// value is round-tripped in every position a design can declare one: a
// required field, a set optional, an unset optional, a field of a nested
// struct, and the elements of an array. Alongside them ride plain
// string, int and bool fields, which must decode as they always did.
//
// Every value must be a VALUE. The format's own null goes to [RunNull],
// which pins the one place it behaves differently.
func RunWith(t *testing.T, c Codec, values ...[]byte) {
	t.Helper()
	runWith(t, c, values...)
}

// RunNull round-trips the format's own null in a REQUIRED raw field,
// where it is a value like any other and must come back as the bytes
// that spell it.
//
// It is checked apart because an OPTIONAL raw field is a pointer, and a
// pointer is also how a Go codec spells "this was not there": decoding
// the format's null into one leaves it nil, so a field SET to null and a
// field never set read alike. Distinguishing them is what a required
// field is for, and what this checks.
func RunNull(t *testing.T, c Codec, null []byte) {
	t.Helper()
	runNull(t, c, null)
}

// ---- the checks, separable from the reporting -------------------------

// runWith is [RunWith] against the reduced [reporter] so the suite's own
// tests can read its failures back instead of raising them.
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

// runNull is [RunNull] against the reduced [reporter].
func runNull(t reporter, c Codec, null []byte) {
	t.Helper()
	for _, problem := range checkNull(c, null) {
		t.Errorf("codec %s, carrying the null %s: %s", c.Name(), null, problem)
	}
}

// checkValue round-trips one value through every raw-carrying shape and
// returns what did not survive - empty when the codec honoured the
// contract. Returning the failures rather than raising them is what
// makes the suite checkable by its own tests.
func checkValue(c Codec, value []byte) []string {
	out, problems := roundTrip(c, value)
	if len(problems) > 0 {
		return problems
	}
	problems = append(problems, samenessProblems(value, out)...)
	if out.Extra == nil {
		problems = append(problems, "a set optional raw field came back absent")
	} else if string(*out.Extra) != string(value) {
		problems = append(problems, fmt.Sprintf(
			"a set optional raw field came back as %s", *out.Extra))
	}
	return problems
}

// checkNull is [checkValue] for the format's null: the required field
// must keep it, and the optional field is expected to read as absent -
// which is the documented shape, not a failure.
func checkNull(c Codec, null []byte) []string {
	out, problems := roundTrip(c, null)
	if len(problems) > 0 {
		return problems
	}
	return append(problems, samenessProblems(null, out)...)
}

// samenessProblems reports every raw slot of a decoded payload that did
// not come back as want. The optional slot is left to the caller: it is
// the one whose answer depends on whether the value is the format's null.
func samenessProblems(want []byte, out payload) []string {
	var problems []string
	same := func(where string, got wire.Raw) {
		if string(got) != string(want) {
			problems = append(problems, fmt.Sprintf("%s came back as %s", where, got))
		}
	}
	same("a required raw field", out.Body)
	same("a raw field of a nested struct", out.Nested.Doc)
	if out.Missing != nil {
		problems = append(problems, fmt.Sprintf(
			"an unset optional raw field came back as %s - absence is not a value", *out.Missing))
	}
	if len(out.List) != 2 {
		problems = append(problems, fmt.Sprintf(
			"an array of raw values came back with %d element(s), want 2", len(out.List)))
		return problems
	}
	for _, got := range out.List {
		same("an element of an array of raw values", got)
	}
	return problems
}

// roundTrip encodes a payload carrying value and decodes it back. The
// plain fields ride along and are checked here: a codec that carries raw
// bytes by breaking everything else has not passed.
func roundTrip(c Codec, value []byte) (payload, []string) {
	extra := wire.Raw(value)
	in := payload{
		ID:      "evt-1",
		Attempt: 3,
		Ok:      true,
		Body:    wire.Raw(value),
		Extra:   &extra,
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
