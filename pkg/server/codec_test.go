package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"testing/iotest"
	"time"
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

// TrailingData returns the error of a failed read rather than taking it for the end of the body.
func TestTrailingDataReturnsAFailedRead(t *testing.T) {
	broken := errors.New("connection reset")
	if err := TrailingData(strings.NewReader(" \n"), iotest.ErrReader(broken)); !errors.Is(err, broken) {
		t.Errorf("TrailingData = %v, want the read error", err)
	}
	if err := TrailingData(strings.NewReader(" \n"), strings.NewReader("\t\r")); err != nil {
		t.Errorf("whitespace to the end must pass: %v", err)
	}
}

// A strict body read past its cap answers 413, even when what the cap lets through is a whole
// value and whitespace, and the body has no Content-Length.
func TestStrictDecodeAnswersABodyPastItsCap(t *testing.T) {
	resetCodec(t)
	if err := SetStrictJSON(true); err != nil {
		t.Fatal(err)
	}
	s := New(nil)
	s.Handle("POST /items", WithLimits(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Name string `json:"name"`
		}
		if err := JSON().Decode(r.Body, &v); err != nil {
			WriteValidationError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}), Limits{MaxBodySize: 64}))
	body := `{"name":"n"}` + strings.Repeat(" ", 60) + "trailing data"
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/items", io.MultiReader(strings.NewReader(body))))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("a %d-byte body past a 64-byte cap: %d %s, want 413", len(body), rec.Code, rec.Body)
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

// wireShape is a request type with the shapes whose decode errors name a path: a defined
// string type, an embedded struct, a nested struct, a pointer and slices.
type wireShape struct {
	wireKeyed
	WireLabel
	Kind   wireKind       `json:"kind"`
	Backup *string        `json:"backup_email,omitempty"`
	Body   wireInner      `json:"body"`
	Tags   []string       `json:"tags"`
	Items  []wireInner    `json:"items"`
	ByID   map[int]int    `json:"byId"`
	Blob   []byte         `json:"blob"`
	Opt    *wireInner     `json:"opt,omitempty"`
	Ratio  float32        `json:"ratio"`
	Counts map[string]int `json:"counts"`
	Quoted int            `json:"quoted,string"`
	Scale  float64        `json:"scale,string"`
}

type wireKind string

// WireLabel is an embedded field that is no struct, which encoding/json keeps under its name.
type WireLabel string

type wireKeyed struct {
	Primary *string `json:"primary_email,omitempty"`
}

type wireInner struct {
	N   int8   `json:"n"`
	Sku string `json:"sku"`
}

// A type mismatch the built-in codec decodes is reported under the field's JSON path, the
// embedded structs left out, as "<path>: <reason>", naming no Go struct or type; the
// *json.UnmarshalTypeError stays in the chain.
func TestDecodeTypeErrorsNameTheWirePath(t *testing.T) {
	for body, want := range map[string]string{
		`{"kind": 5}`:                "kind: expected string, got number",
		`{"primary_email": 5}`:       "primary_email: expected string, got number",
		`{"backup_email": ["a"]}`:    "backup_email: expected string, got array",
		`{"body": {"n": 300}}`:       "body.n: 300 is out of range",
		`{"body": {"n": 1.5}}`:       "body.n: expected integer, got number 1.5",
		`{"body": "x"}`:              "body: expected object, got string",
		`{"tags": "x"}`:              "tags: expected array, got string",
		`{"items": [{"sku": true}]}`: "items.sku: expected string, got boolean",
		`{"byId": {"x": 1}}`:         `byId: "x" is not an integer`,
		`{"WireLabel": 5}`:           "WireLabel: expected string, got number",
		`{"quoted": "1x"}`:           `quoted: "1x" is not an integer`,
		`{"scale": "1x"}`:            `scale: "1x" is not a number`,
		`{"blob": 5}`:                "blob: expected string, got number",
		`{"opt": {"n": "x"}}`:        "opt.n: expected integer, got string",
		`{"ratio": 1e39}`:            "ratio: 1e39 is out of range",
		`{"counts": {"a": "x"}}`:     "counts: expected integer, got string",
		`"x"`:                        "body: expected object, got string",
	} {
		for name, decode := range map[string]func(io.Reader, any) error{
			"Decode":       defaultCodec{}.Decode,
			"DecodeStrict": defaultCodec{}.DecodeStrict,
		} {
			var v wireShape
			err := decode(strings.NewReader(body), &v)
			if err == nil || err.Error() != want {
				t.Errorf("%s(%s) = %v, want %q", name, body, err, want)
				continue
			}
			var te *json.UnmarshalTypeError
			if !errors.As(err, &te) {
				t.Errorf("%s(%s): the *json.UnmarshalTypeError left the chain", name, body)
			}
		}
	}
}

// wireStamped holds values encoding/json hands to their own code: datetimes, base64 bytes and a
// type that unmarshals itself, beside a raw value.
type wireStamped struct {
	At    time.Time            `json:"at"`
	AtP   *time.Time           `json:"atp"`
	Files []wireFile           `json:"files"`
	ByDay map[string]time.Time `json:"byDay"`
	Level wireLevel            `json:"level"`
	Raw   json.RawMessage      `json:"raw"`
}

type wireFile struct {
	Data []byte `json:"data"`
}

// wireLevel unmarshals itself from a JSON string.
type wireLevel int

func (l *wireLevel) UnmarshalText(b []byte) error {
	if string(b) != "high" {
		return errors.New("unknown level")
	}
	*l = 1
	return nil
}

// A value encoding/json hands to its own code fails under its JSON path whichever engine
// decodes, a datetime and base64 bytes in the words of their format checks, another type in its
// own; the decoder's error stays in the chain.
func TestDecodeFailuresOfSelfDecodedValuesNameTheWirePath(t *testing.T) {
	for _, c := range []struct {
		body, want string
		cause      any
	}{
		{`{"at": 1}`, "at: expected string, got number", nil},
		{`{"at": "tomorrow"}`, "at: not a valid RFC 3339 datetime", new(*time.ParseError)},
		{`{"AT": "tomorrow"}`, "at: not a valid RFC 3339 datetime", new(*time.ParseError)},
		{`{"at": "2024-01-02T03:04:05Z", "at": "x"}`, "at: not a valid RFC 3339 datetime", new(*time.ParseError)},
		{`{"atp": true}`, "atp: expected string, got boolean", nil},
		{`{"raw": {"at": [1]}, "files": [{"data": "aGk="}, {"data": "QUI"}]}`, "files.data: not a valid base64", new(base64.CorruptInputError)},
		{`{"byDay": {"mon": "x"}}`, "byDay: not a valid RFC 3339 datetime", new(*time.ParseError)},
		{`{"level": "low"}`, "level: unknown level", nil},
	} {
		for name, decode := range map[string]func(io.Reader, any) error{
			"Decode":       defaultCodec{}.Decode,
			"DecodeStrict": defaultCodec{}.DecodeStrict,
		} {
			var v wireStamped
			err := decode(strings.NewReader(c.body), &v)
			if err == nil || err.Error() != c.want {
				t.Errorf("%s(%s) = %v, want %q", name, c.body, err, c.want)
				continue
			}
			if c.cause != nil && !errors.As(err, c.cause) {
				t.Errorf("%s(%s): the decoder's %T left the chain", name, c.body, c.cause)
			}
		}
	}
	var at time.Time
	if err := (defaultCodec{}).Decode(strings.NewReader(`"x"`), &at); err == nil || err.Error() != "body: not a valid RFC 3339 datetime" {
		t.Errorf(`Decode("x") into a time.Time = %v, want the body named`, err)
	}
}

// A body is kept to find a failed value in only when its type can hold one encoding/json hands
// to the value's own code; a raw value takes any JSON.
func TestDecodeKeepsTheBodyOnlyForSelfDecodedValues(t *testing.T) {
	for typ, want := range map[reflect.Type]bool{
		reflect.TypeFor[wireStamped]():                   true,
		reflect.TypeFor[*wireShape]():                    true,
		reflect.TypeFor[wireNode]():                      false,
		reflect.TypeFor[struct{ R json.RawMessage }]():   false,
		reflect.TypeFor[map[string][]*wireStampedNode](): true,
	} {
		if got := holdsSelfDecoding(typ); got != want {
			t.Errorf("holdsSelfDecoding(%v) = %v, want %v", typ, got, want)
		}
	}
}

// An empty body is required, io.EOF left in the chain.
func TestDecodeOfAnEmptyBodyIsRequired(t *testing.T) {
	for _, body := range []string{"", " \n\t"} {
		for name, decode := range map[string]func(io.Reader, any) error{
			"Decode":       defaultCodec{}.Decode,
			"DecodeStrict": defaultCodec{}.DecodeStrict,
		} {
			var v wireStamped
			err := decode(strings.NewReader(body), &v)
			if err == nil || err.Error() != "body: required" || !errors.Is(err, io.EOF) {
				t.Errorf("%s(%q) = %v, want body: required wrapping io.EOF", name, body, err)
			}
		}
	}
}

// WireMeta is a struct embedded beside a JSON key spelled as its Go name.
type WireMeta struct {
	N int `json:"n"`
}

type wireClash struct {
	WireMeta
	Meta int `json:"WireMeta"`
}

type wireClashLast struct {
	Meta struct {
		Q int `json:"q"`
	} `json:"WireMeta"`
	WireMeta
}

// WireAudit is embedded after a field whose JSON key is its Go name and that holds a "by" too.
type WireAudit struct {
	By string `json:"by"`
}

type wireClashKeyFirst struct {
	Prev struct {
		By string `json:"by"`
	} `json:"WireAudit"`
	WireAudit
}

type wireHidden struct {
	hidden int
	Shown  wireClash `json:"hidden"`
}

// A JSON key spelled as the Go name of an embedded struct is reported as that key, and a field
// of the embedded struct under its own key, whichever the struct declares first.
func TestDecodePathsTellAJSONKeyFromAnEmbeddedName(t *testing.T) {
	for _, c := range []struct {
		v          any
		body, want string
	}{
		{&wireClash{}, `{"WireMeta": "x"}`, "WireMeta: expected integer, got string"},
		{&wireClash{}, `{"n": "x"}`, "n: expected integer, got string"},
		{&wireClashLast{}, `{"WireMeta": {"q": "x"}}`, "WireMeta.q: expected integer, got string"},
		{&wireClashLast{}, `{"n": "x"}`, "n: expected integer, got string"},
		{&wireClashKeyFirst{}, `{"WireAudit": {"by": 5}}`, "WireAudit.by: expected string, got number"},
		{&wireClashKeyFirst{}, `{"by": 5}`, "by: expected string, got number"},
		{&wireHidden{hidden: 1}, `{"hidden": {"n": "x"}}`, "hidden.n: expected integer, got string"},
	} {
		if err := (defaultCodec{}).Decode(strings.NewReader(c.body), c.v); err == nil || err.Error() != c.want {
			t.Errorf("Decode(%s) into %T = %v, want %q", c.body, c.v, err, c.want)
		}
	}
}

type wireNode struct {
	Child *wireNode `json:"c"`
	V     int       `json:"v"`
}

type wireStampedNode struct {
	Child *wireStampedNode `json:"c"`
	At    time.Time        `json:"at"`
}

// The path of a decode error deep in a recursive body, a datetime's included, costs memory in
// proportion to its depth.
func TestDecodePathOfADeepBodyCostsLinearMemory(t *testing.T) {
	const depth = 4000
	for _, c := range []struct {
		leaf, why string
		v         any
	}{
		{`{"v":"x"}`, "v: expected integer, got string", &wireNode{}},
		{`{"at":"x"}`, "at: not a valid RFC 3339 datetime", &wireStampedNode{}},
	} {
		body := strings.Repeat(`{"c":`, depth) + c.leaf + strings.Repeat("}", depth)
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		err := defaultCodec{}.Decode(strings.NewReader(body), c.v)
		runtime.ReadMemStats(&after)
		if want := strings.Repeat("c.", depth) + c.why; err == nil || err.Error() != want {
			t.Fatalf("Decode into %T = %.40v..., want the path of %d keys", c.v, err, depth+1)
		}
		if grew := after.TotalAlloc - before.TotalAlloc; grew > 32<<20 {
			t.Errorf("decoding a body %d deep into %T allocated %d MB", depth, c.v, grew>>20)
		}
	}
}

// WireLoop embeds itself and holds a field keyed by its own name.
type WireLoop struct {
	*WireLoop
	X *WireLoop `json:"WireLoop"`
}

type wireLoopHost struct {
	*WireLoop
	Next *wireLoopHost `json:"WireLoop"`
	Leaf int           `json:"leaf"`
}

// The path through a type that embeds itself is found without trying every reading of it.
func TestDecodePathThroughASelfEmbeddingTypeIsFoundOnce(t *testing.T) {
	const depth = 16
	body := strings.Repeat(`{"WireLoop":`, depth) + `{"leaf":"x"}` + strings.Repeat("}", depth)
	want := strings.Repeat("WireLoop.", depth) + "leaf: expected integer, got string"
	var err error
	allocs := testing.AllocsPerRun(5, func() {
		err = defaultCodec{}.Decode(strings.NewReader(body), &wireLoopHost{})
	})
	if err == nil || err.Error() != want {
		t.Errorf("Decode = %v, want %q", err, want)
	}
	if allocs > 2000 {
		t.Errorf("decoding a body %d deep allocated %.0f times", depth, allocs)
	}
}
