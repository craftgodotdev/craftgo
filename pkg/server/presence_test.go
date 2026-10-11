package server

import (
	"strings"
	"testing"
)

// nullableBody is a body with a member that may be null, as a generated wire struct holds one.
type nullableBody struct {
	N Nullable[int]           `json:"n"`
	S Nullable[nullableInner] `json:"s"`
}

type nullableInner struct {
	A int `json:"a"`
}

// A Nullable tells an absent member from a null one and from a value.
func TestNullableTellsAbsentFromNull(t *testing.T) {
	for _, c := range []struct {
		body     string
		present  bool
		wantNil  bool
		wantN    int
		wantSNil bool
	}{
		{`{}`, false, true, 0, true},
		{`{"n":null,"s":null}`, true, true, 0, true},
		{`{"n": null }`, true, true, 0, true},
		{`{"n":0,"s":{"a":2}}`, true, false, 0, false},
		{`{"n":7,"s":{"a":2}}`, true, false, 7, false},
	} {
		var b nullableBody
		if err := JSON().Decode(strings.NewReader(c.body), &b); err != nil {
			t.Fatalf("%s: %v", c.body, err)
		}
		if b.N.Present() != c.present || (b.N.Value() == nil) != c.wantNil || (b.S.Value() == nil) != c.wantSNil {
			t.Errorf("%s: present %v, value %v, s %v", c.body, b.N.Present(), b.N.Value(), b.S.Value())
			continue
		}
		if v := b.N.Value(); v != nil && *v != c.wantN {
			t.Errorf("%s: n = %d, want %d", c.body, *v, c.wantN)
		}
	}
}

// A value of the wrong type in a Nullable is reported at the member's path, inside it too.
func TestNullableTypeErrorNamesTheMember(t *testing.T) {
	for body, want := range map[string]string{
		`{"n":"x"}`:       `n: expected integer, got string`,
		`{"s":{"a":"x"}}`: `s.a: expected integer, got string`,
	} {
		var b nullableBody
		err := JSON().Decode(strings.NewReader(body), &b)
		if err == nil || err.Error() != want {
			t.Errorf("%s: got %v, want %q", body, err, want)
		}
	}
}

// A missing member's error names its JSON path.
func TestMissingMember(t *testing.T) {
	if got := MissingMember("items.sku").Error(); got != "items.sku: required" {
		t.Errorf("got %q", got)
	}
}
