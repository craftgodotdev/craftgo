// bind_bench_test.go — pure parse benchmarks: codegen vs reflect at
// two request-shape sizes.
//
// Two shape buckets cover how the cost scales with field count:
//
//   simpleReq  (2 fields — path + query int, no body):
//     GET /users/{id}?limit=10
//
//   complexReq (9 fields — path + 4 query, header, cookie + JSON body):
//     POST /orders/{id}/checkout?limit=10&dryRun=true&tags=...&years=...
//
// For each size both parsers populate the SAME struct from the SAME
// pre-extracted [inputSet]; the benchmarks isolate the parse +
// write-back work, with no http.Request, url.ParseQuery,
// http.Cookie or header machinery in the hot loop.
//
// Run: go test -bench BenchmarkParse -benchmem -count=3 ./internal/bench

package bench

import (
	"encoding/json"
	"reflect"
	"strconv"
	"testing"
)

// ---------- simple shape ----------

// simpleReq is a minimal GET-by-id-with-limit endpoint: one path
// segment + one query int. This is the floor of how many fields a
// real handler ever binds.
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

// codegenParseSimple is what the craftgo template would emit for
// simpleReq if you stripped the http machinery — just two field
// writes.
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

// ---------- complex shape ----------

// complexReq is the kitchen-sink: every binding source represented,
// scalar + array + numeric + bool field shapes mixed in.
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

// codegenParseComplex mirrors what the craftgo handler template
// emits today, with the http plumbing factored out.
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

// ---------- shared types ----------

// inputSet is the pre-extracted values both parsers consume. body
// may be empty; the JSON-decode step is gated on len(body) > 0 so
// the same parsers work for endpoints with no body too.
type inputSet struct {
	path   map[string]string
	query  map[string][]string
	header map[string]string
	cookie map[string]string
	body   []byte
}

// reflectParse is shape-agnostic: walks any struct passed in,
// dispatching on each field's tag to decide which inputSet bucket
// to read. This is what every popular Go web framework's BindXxx
// helper does at runtime.
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

// setReflect writes one or more string values into fv. Slices use
// MakeSlice + Append; scalars dispatch to setScalar. Empty input
// is a no-op so absent values leave the field at its zero value.
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

// setScalar parses s into fv via strconv. Field kinds outside
// string/bool/int*/uint*/float* are silently skipped — same as
// the codegen, which rejects them at gen time.
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

// bindSink is the package-level escape hatch every benchmark writes
// its final struct to. Without it the compiler is free to dead-store-
// eliminate field writes — `req` would otherwise be observably dead
// after the loop body. A real handler would dispatch the struct to
// user logic; bindSink stands in for that.
var bindSink any

// ---------- simple-shape benchmarks ----------

// BenchmarkParseSimpleCodegen pins the codegen cost for the floor
// case: 2 fields, no body. With this few fields the reflect tax
// is at its most defensible — the test exists to show whether
// codegen still wins when the field budget is small.
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

// BenchmarkParseSimpleReflect is the reflect twin: same 2 fields,
// reflect.ValueOf + tag walk + setReflect.
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

// ---------- complex-shape benchmarks ----------

// BenchmarkParseComplexCodegen is the kitchen-sink codegen path: 9
// fields including arrays and a JSON body.
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

// BenchmarkParseComplexReflect is the kitchen-sink reflect twin.
// Pair its number with BenchmarkParseComplexCodegen for the
// dominant data point in the report — most real handlers sit
// closer to this shape than to simpleReq.
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

// BenchmarkParseComplexBodyOnly is the JSON-decode baseline for
// the complex shape. Both complex parsers pay this cost; subtract
// it to reason about the per-field non-body work in isolation.
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

// ---------- correctness gates ----------

// TestParseEquivalenceSimple is the simple-shape correctness gate:
// codegen and reflect must produce identical structs from the same
// fixture. Without this any benchmark delta is meaningless — fast
// wrong code is the fastest code.
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

// TestParseEquivalenceComplex is the complex-shape correctness gate.
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
