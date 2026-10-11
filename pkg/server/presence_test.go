package server

import (
	"strings"
	"testing"
	"time"
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

// nullableBase is a mixin a wire struct embeds.
type nullableBase struct {
	N int `json:"n"`
}

// nullableMixed holds a mixin's member as its own.
type nullableMixed struct {
	nullableBase
	K int `json:"k"`
}

// nullableShapes holds a Nullable around each shape a member takes.
type nullableShapes struct {
	nullableBody
	E  Nullable[nullableMixed]                `json:"e"`
	L  Nullable[[]nullableInner]              `json:"l"`
	M  Nullable[map[string]nullableInner]     `json:"m"`
	NN Nullable[nullableBody]                 `json:"nn"`
	T  Nullable[time.Time]                    `json:"t"`
	LL []Nullable[map[string][]nullableInner] `json:"ll"`
}

// A value that fails to decode in a Nullable is reported at its JSON path, by JSON names alone,
// whatever it is held in.
func TestNullableFailureNamesItsPath(t *testing.T) {
	for body, want := range map[string]string{
		`{"n":"x"}`:                  `n: expected integer, got string`,
		`{"s":{"a":"x"}}`:            `s.a: expected integer, got string`,
		`{"e":{"n":true,"k":1}}`:     `e.n: expected integer, got boolean`,
		`{"l":[{"a":1},{"a":[]}]}`:   `l.a: expected integer, got array`,
		`{"m":{"secret":{"a":"x"}}}`: `m.a: expected integer, got string`,
		`{"nn":{"s":{"a":"x"}}}`:     `nn.s.a: expected integer, got string`,
		`{"nn":{"n":{}}}`:            `nn.n: expected integer, got object`,
		`{"t":"soon"}`:               `t: not a valid RFC 3339 datetime`,
		`{"ll":[{"k":[{"a":"x"}]}]}`: `ll.a: expected integer, got string`,
	} {
		var b nullableShapes
		err := JSON().Decode(strings.NewReader(body), &b)
		if err == nil || err.Error() != want {
			t.Errorf("%s: got %v, want %q", body, err, want)
		}
	}
}

// An unknown member inside a Nullable is named as at any other depth.
func TestNullableUnknownMember(t *testing.T) {
	if err := SetStrictJSON(true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = SetStrictJSON(false) })
	var b nullableBody
	err := JSON().Decode(strings.NewReader(`{"s":{"a":1,"zzz":1}}`), &b)
	if err == nil || err.Error() != "zzz: unknown field" {
		t.Errorf("got %v, want zzz: unknown field", err)
	}
}

// A missing member's error names its JSON path.
func TestMissingMember(t *testing.T) {
	if got := MissingMember("items.sku").Error(); got != "items.sku: required" {
		t.Errorf("got %q", got)
	}
}
