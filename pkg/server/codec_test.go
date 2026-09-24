package server

import (
	"io"
	"strings"
	"testing"
)

func TestDecodeStrictRejectsUnknownFieldsAndTrailingData(t *testing.T) {
	type req struct {
		Name string `json:"name"`
	}
	var v req
	c := defaultCodec{}
	err := c.DecodeStrict(strings.NewReader(`{"name":"a","nmae":"b"}`), &v)
	if err == nil || err.Error() != "nmae: unknown field" {
		t.Errorf("unknown field: err = %v", err)
	}
	for _, body := range []string{`{"name":"a"} {"name":"b"}`, `{"name":"a"} junk`} {
		err := c.DecodeStrict(strings.NewReader(body), &v)
		if err == nil || !strings.Contains(err.Error(), "after the JSON value") {
			t.Errorf("%q: err = %v", body, err)
		}
	}
	if err := c.DecodeStrict(strings.NewReader("{\"name\":\"a\"}\n"), &v); err != nil {
		t.Errorf("trailing whitespace must pass: %v", err)
	}
	if err := c.DecodeStrict(strings.NewReader(`{"name":`), &v); err == nil {
		t.Error("malformed JSON must still fail")
	}
	if err := c.Decode(strings.NewReader(`{"name":"a","nmae":"b"} junk`), &v); err != nil || v.Name != "a" {
		t.Errorf("lenient Decode must ignore unknown fields and trailing data: err = %v, name = %q", err, v.Name)
	}
}

// lenientOnly is a codec without DecodeStrict.
type lenientOnly struct{}

func (lenientOnly) Encode(io.Writer, any) error { return nil }
func (lenientOnly) Decode(io.Reader, any) error { return nil }

// spyCodec counts which decode path JSON() routes to.
type spyCodec struct{ lenient, strict *int }

func (spyCodec) Encode(io.Writer, any) error         { return nil }
func (c spyCodec) Decode(io.Reader, any) error       { *c.lenient++; return nil }
func (c spyCodec) DecodeStrict(io.Reader, any) error { *c.strict++; return nil }

func resetCodec(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		_ = SetStrictJSON(false)
		_ = SetGlobalJSONCodec(nil)
	})
}

// With strict on, JSON() decodes through DecodeStrict; off, through Decode.
func TestStrictJSONRoutesThroughDecodeStrict(t *testing.T) {
	resetCodec(t)
	lenient, strict := 0, 0
	if err := SetGlobalJSONCodec(spyCodec{&lenient, &strict}); err != nil {
		t.Fatal(err)
	}
	if err := SetStrictJSON(true); err != nil {
		t.Fatal(err)
	}
	_ = JSON().Decode(strings.NewReader(`{}`), &struct{}{})
	if err := SetStrictJSON(false); err != nil {
		t.Fatal(err)
	}
	_ = JSON().Decode(strings.NewReader(`{}`), &struct{}{})
	if lenient != 1 || strict != 1 {
		t.Errorf("decode paths: lenient %d, strict %d, want 1 and 1", lenient, strict)
	}
}

// A codec without DecodeStrict is refused in either order, and the previous state stays.
func TestStrictJSONRefusesACodecWithoutDecodeStrict(t *testing.T) {
	resetCodec(t)
	if err := SetGlobalJSONCodec(lenientOnly{}); err != nil {
		t.Fatal(err)
	}
	if err := SetStrictJSON(true); err == nil {
		t.Fatal("SetStrictJSON(true) with a lenient-only codec must fail")
	}
	if _, ok := JSON().(lenientOnly); !ok {
		t.Errorf("the lenient codec must stay installed, got %T", JSON())
	}
	if err := SetGlobalJSONCodec(nil); err != nil {
		t.Fatal(err)
	}
	if err := SetStrictJSON(true); err != nil {
		t.Fatal(err)
	}
	if err := SetGlobalJSONCodec(lenientOnly{}); err == nil {
		t.Fatal("installing a lenient-only codec while strict is on must fail")
	}
	if _, ok := JSON().(strictCodec); !ok {
		t.Errorf("the strict built-in codec must stay installed, got %T", JSON())
	}
}
