package bench

import (
	"encoding/json"
	"reflect"
	"strconv"
	"testing"
)

// simpleReq is `GET /users/{id}?limit=10`: a path string and a query int.
type simpleReq struct {
	ID    string `path:"id"`
	Limit int    `query:"limit"`
}

func simpleFixture() *inputSet {
	return &inputSet{
		path:  map[string]string{"id": "u-42"},
		query: map[string][]string{"limit": {"10"}},
	}
}

// codegenParseSimple binds simpleReq with direct field writes and strconv.
func codegenParseSimple(in *inputSet, req *simpleReq) error {
	req.ID = in.path["id"]
	if v := in.query["limit"]; len(v) > 0 && v[0] != "" {
		n, err := strconv.ParseInt(v[0], 10, 64)
		if err != nil {
			return err
		}
		req.Limit = int(n)
	}
	return nil
}

// complexReq is `POST /orders/{id}/checkout` with query, header, cookie and
// JSON body fields, scalars and arrays.
type complexReq struct {
	ID             string        `path:"id"`
	Limit          int           `query:"limit"`
	DryRun         bool          `query:"dryRun"`
	Tags           []string      `query:"tags"`
	Years          []int         `query:"years"`
	Notes          string        `json:"notes"`
	Lines          []bindLineReq `json:"lines"`
	IdempotencyKey string        `header:"idempotencyKey"`
	SessionID      string        `cookie:"sessionId"`
}

type bindLineReq struct {
	BookID   string `json:"bookId"`
	Quantity int    `json:"quantity"`
}

func complexFixture() *inputSet {
	return &inputSet{
		path: map[string]string{"id": "draft-99"},
		query: map[string][]string{
			"limit":  {"10"},
			"dryRun": {"true"},
			"tags":   {"fiction", "ya"},
			"years":  {"2020", "2021"},
		},
		header: map[string]string{"idempotencyKey": "key-abc"},
		cookie: map[string]string{"sessionId": "sess-xyz"},
		body:   []byte(`{"notes":"ship gift-wrapped","lines":[{"bookId":"b1","quantity":2},{"bookId":"b2","quantity":1}]}`),
	}
}

// codegenParseComplex decodes the body, then binds the other complexReq fields
// with direct field writes and strconv.
func codegenParseComplex(in *inputSet, req *complexReq) error {
	if err := json.Unmarshal(in.body, req); err != nil {
		return err
	}
	req.ID = in.path["id"]
	if v := in.query["limit"]; len(v) > 0 && v[0] != "" {
		n, err := strconv.ParseInt(v[0], 10, 64)
		if err != nil {
			return err
		}
		req.Limit = int(n)
	}
	if v := in.query["dryRun"]; len(v) > 0 && v[0] != "" {
		bv, err := strconv.ParseBool(v[0])
		if err != nil {
			return err
		}
		req.DryRun = bv
	}
	req.Tags = in.query["tags"]
	for _, v := range in.query["years"] {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return err
		}
		req.Years = append(req.Years, int(n))
	}
	req.IdempotencyKey = in.header["idempotencyKey"]
	req.SessionID = in.cookie["sessionId"]
	return nil
}

// inputSet holds the request values both parsers read, already extracted.
type inputSet struct {
	path   map[string]string
	query  map[string][]string
	header map[string]string
	cookie map[string]string
	body   []byte
}

// reflectParse binds any struct, reading for each field the inputSet map its
// tag names; a non-empty body is decoded first.
func reflectParse(in *inputSet, req any) error {
	if len(in.body) > 0 {
		if err := json.Unmarshal(in.body, req); err != nil {
			return err
		}
	}
	rv := reflect.ValueOf(req).Elem()
	rt := rv.Type()
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		fv := rv.Field(i)
		switch {
		case f.Tag.Get("path") != "":
			if err := setReflect(fv, []string{in.path[f.Tag.Get("path")]}); err != nil {
				return err
			}
		case f.Tag.Get("query") != "":
			if err := setReflect(fv, in.query[f.Tag.Get("query")]); err != nil {
				return err
			}
		case f.Tag.Get("header") != "":
			if err := setReflect(fv, []string{in.header[f.Tag.Get("header")]}); err != nil {
				return err
			}
		case f.Tag.Get("cookie") != "":
			if err := setReflect(fv, []string{in.cookie[f.Tag.Get("cookie")]}); err != nil {
				return err
			}
		}
	}
	return nil
}

// setReflect writes vals into fv, a slice or a scalar; no values leave fv as is.
func setReflect(fv reflect.Value, vals []string) error {
	if fv.Kind() == reflect.Slice {
		if len(vals) == 0 {
			return nil
		}
		elem := fv.Type().Elem()
		out := reflect.MakeSlice(fv.Type(), 0, len(vals))
		for _, s := range vals {
			ev := reflect.New(elem).Elem()
			if err := setScalar(ev, s); err != nil {
				return err
			}
			out = reflect.Append(out, ev)
		}
		fv.Set(out)
		return nil
	}
	if len(vals) == 0 {
		return nil
	}
	return setScalar(fv, vals[0])
}

// setScalar parses s into fv; an empty s or another kind leaves fv as is.
func setScalar(fv reflect.Value, s string) error {
	if s == "" {
		return nil
	}
	switch fv.Kind() {
	case reflect.String:
		fv.SetString(s)
	case reflect.Bool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return err
		}
		fv.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return err
		}
		fv.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return err
		}
		fv.SetUint(n)
	case reflect.Float32, reflect.Float64:
		n, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return err
		}
		fv.SetFloat(n)
	}
	return nil
}

// bindSink receives each benchmark's result so the compiler cannot eliminate
// the binding.
var bindSink any

func BenchmarkParseSimpleCodegen(b *testing.B) {
	in := simpleFixture()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var req simpleReq
		if err := codegenParseSimple(in, &req); err != nil {
			b.Fatal(err)
		}
		bindSink = req
	}
}

func BenchmarkParseSimpleReflect(b *testing.B) {
	in := simpleFixture()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var req simpleReq
		if err := reflectParse(in, &req); err != nil {
			b.Fatal(err)
		}
		bindSink = req
	}
}

func BenchmarkParseComplexCodegen(b *testing.B) {
	in := complexFixture()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var req complexReq
		if err := codegenParseComplex(in, &req); err != nil {
			b.Fatal(err)
		}
		bindSink = req
	}
}

func BenchmarkParseComplexReflect(b *testing.B) {
	in := complexFixture()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var req complexReq
		if err := reflectParse(in, &req); err != nil {
			b.Fatal(err)
		}
		bindSink = req
	}
}

// BenchmarkParseComplexBodyOnly measures the JSON decode both complex parsers
// include.
func BenchmarkParseComplexBodyOnly(b *testing.B) {
	in := complexFixture()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var req complexReq
		if err := json.Unmarshal(in.body, &req); err != nil {
			b.Fatal(err)
		}
		bindSink = req
	}
}

// TestParseEquivalenceSimple checks that both parsers bind simpleReq alike.
func TestParseEquivalenceSimple(t *testing.T) {
	in := simpleFixture()
	var a, b simpleReq
	if err := codegenParseSimple(in, &a); err != nil {
		t.Fatal(err)
	}
	if err := reflectParse(in, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("simple parse diverged:\ncodegen=%+v\nreflect=%+v", a, b)
	}
}

// TestParseEquivalenceComplex checks that both parsers bind complexReq alike.
func TestParseEquivalenceComplex(t *testing.T) {
	in := complexFixture()
	var a, b complexReq
	if err := codegenParseComplex(in, &a); err != nil {
		t.Fatal(err)
	}
	if err := reflectParse(in, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("complex parse diverged:\ncodegen=%+v\nreflect=%+v", a, b)
	}
}
