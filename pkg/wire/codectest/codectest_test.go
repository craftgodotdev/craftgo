package codectest

import (
	"encoding/json"
	"fmt"
	"testing"
)

// jsonCodec is encoding/json as a [Codec].
type jsonCodec struct{}

func (jsonCodec) Name() string                    { return "json" }
func (jsonCodec) Marshal(v any) ([]byte, error)   { return json.Marshal(v) }
func (jsonCodec) Unmarshal(d []byte, v any) error { return json.Unmarshal(d, v) }

// Run passes a codec that carries Raw unchanged.
func TestRunPassesForAJSONCodec(t *testing.T) {
	Run(t, jsonCodec{})
}

// lossyCodec decodes through `any`, losing big integers, trailing zeros and
// explicit nulls.
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

// recorder collects what the suite reports.
type recorder struct{ msgs []string }

func (*recorder) Helper() {}

func (r *recorder) Errorf(format string, args ...any) {
	r.msgs = append(r.msgs, fmt.Sprintf(format, args...))
}

// The suite fails a lossy codec on each fixture separately.
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

// RunNull fails a codec that decodes a literal null as absent.
func TestRunNullFailsWhenTheNullIsDropped(t *testing.T) {
	var rec recorder
	runNull(&rec, droppingCodec{}, []byte("null"))
	if len(rec.msgs) == 0 {
		t.Error("the suite passed a codec that drops a literal null")
	}
}

// droppingCodec decodes every Raw field as absent.
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

// RunWith with no values reports one failure.
func TestRunWithNoValuesIsAFailure(t *testing.T) {
	var rec recorder
	runWith(&rec, jsonCodec{}, nil...)
	if len(rec.msgs) != 1 {
		t.Errorf("an empty value set reported %v, want one failure", rec.msgs)
	}
}
