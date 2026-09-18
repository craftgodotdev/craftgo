package codectest

import (
	"encoding/json"
	"fmt"
	"testing"
)

// jsonCodec is encoding/json behind the three methods, declared here
// rather than imported: the suite must not depend on a concrete codec,
// and a codec that never heard of craftgo must still satisfy it.
type jsonCodec struct{}

func (jsonCodec) Name() string                    { return "json" }
func (jsonCodec) Marshal(v any) ([]byte, error)   { return json.Marshal(v) }
func (jsonCodec) Unmarshal(d []byte, v any) error { return json.Unmarshal(d, v) }

// The suite passes a codec that honours the contract.
func TestRunPassesForAJSONCodec(t *testing.T) {
	Run(t, jsonCodec{})
}

// lossyCodec is what the suite exists to catch: a codec that decodes
// every value into `any` and re-encodes it, flattening the explicit
// null, the digits past 2^53 and the trailing zero on the way.
type lossyCodec struct{}

func (lossyCodec) Name() string { return "lossy" }

func (lossyCodec) Marshal(v any) ([]byte, error) { return json.Marshal(v) }

func (lossyCodec) Unmarshal(data []byte, v any) error {
	var loose any
	if err := json.Unmarshal(data, &loose); err != nil {
		return err
	}
	round, err := json.Marshal(loose)
	if err != nil {
		return err
	}
	return json.Unmarshal(round, v)
}

// recorder collects what the suite reports instead of failing a test.
type recorder struct{ msgs []string }

func (*recorder) Helper() {}

func (r *recorder) Errorf(format string, args ...any) {
	r.msgs = append(r.msgs, fmt.Sprintf(format, args...))
}

// ... and fails one that does not. Each value is checked on its own so
// a suite that catches one loss is not mistaken for one that catches
// every loss the shape exists to prevent.
func TestRunFailsForALossyCodec(t *testing.T) {
	for _, v := range [][]byte{
		[]byte(`12345678901234567890`),
		[]byte(`1.50`),
		[]byte(`{"explicit":null,"big":12345678901234567890,"trailing":1.50}`),
		[]byte(`["a",{"b":1},2.0]`),
	} {
		var rec recorder
		runWith(&rec, lossyCodec{}, v)
		if len(rec.msgs) == 0 {
			t.Errorf("the suite passed a codec that re-encodes %s", v)
		}
	}
}

// A codec that answers with a value of its own choosing fails the null
// check: a required raw field keeps the four bytes it was handed, and
// "absent" is not one of the answers.
func TestRunNullFailsWhenTheNullIsDropped(t *testing.T) {
	var rec recorder
	runNull(&rec, droppingCodec{}, []byte("null"))
	if len(rec.msgs) == 0 {
		t.Error("the suite passed a codec that drops a literal null")
	}
}

// droppingCodec encodes as JSON but decodes every raw field as absent -
// the collapse a required raw field exists to prevent.
type droppingCodec struct{}

func (droppingCodec) Name() string { return "dropping" }

func (droppingCodec) Marshal(v any) ([]byte, error) { return json.Marshal(v) }

func (droppingCodec) Unmarshal(data []byte, v any) error {
	if err := json.Unmarshal(data, v); err != nil {
		return err
	}
	p, ok := v.(*payload)
	if !ok {
		return nil
	}
	p.Body, p.Extra, p.Nested.Doc, p.List = nil, nil, nil, nil
	return nil
}

// A codec given nothing to carry is not a codec that passed.
func TestRunWithNoValuesIsAFailure(t *testing.T) {
	var rec recorder
	runWith(&rec, jsonCodec{}, nil...)
	if len(rec.msgs) != 1 {
		t.Errorf("an empty value set reported %v, want one failure", rec.msgs)
	}
}
