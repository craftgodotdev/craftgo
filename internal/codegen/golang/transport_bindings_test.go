package golang

import "testing"

// formatToString renders every primitive, bare or named, and reports whether it needs strconv.
func TestFormatToString(t *testing.T) {
	cases := []struct {
		name        string
		prim        string
		named       bool
		wantExpr    string
		wantStrconv bool
	}{
		{"string bare", "string", false, "v", false},
		{"string named", "string", true, "string(v)", false},
		{"bool bare", "bool", false, "strconv.FormatBool(v)", true},
		{"bool named", "bool", true, "strconv.FormatBool(bool(v))", true},
		{"int bare", "int", false, "strconv.Itoa(v)", true},
		{"int named", "int", true, "strconv.FormatInt(int64(v), 10)", true},
		{"int8", "int8", false, "strconv.FormatInt(int64(v), 10)", true},
		{"int16", "int16", false, "strconv.FormatInt(int64(v), 10)", true},
		{"int32", "int32", false, "strconv.FormatInt(int64(v), 10)", true},
		{"int64 bare", "int64", false, "strconv.FormatInt(v, 10)", true},
		{"int64 named", "int64", true, "strconv.FormatInt(int64(v), 10)", true},
		{"uint bare", "uint", false, "strconv.FormatUint(uint64(v), 10)", true},
		{"uint32", "uint32", false, "strconv.FormatUint(uint64(v), 10)", true},
		{"uint64 bare", "uint64", false, "strconv.FormatUint(v, 10)", true},
		{"uint64 named", "uint64", true, "strconv.FormatUint(uint64(v), 10)", true},
		{"float32", "float32", false, "strconv.FormatFloat(float64(v), 'g', -1, 32)", true},
		{"float64 bare", "float64", false, "strconv.FormatFloat(v, 'g', -1, 64)", true},
		{"float64 named", "float64", true, "strconv.FormatFloat(float64(v), 'g', -1, 64)", true},
		{"unknown prim", "widget", false, "v", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			expr, strconv := formatToString(c.prim, c.named, "v")
			if expr != c.wantExpr || strconv != c.wantStrconv {
				t.Errorf("formatToString(%q, %v, \"v\") = (%q, %v), want (%q, %v)",
					c.prim, c.named, expr, strconv, c.wantExpr, c.wantStrconv)
			}
		})
	}
}
