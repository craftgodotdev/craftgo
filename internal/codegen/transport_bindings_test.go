package codegen

import "testing"

// formatToString drives response @header / @cookie value formatting; the codegen
// pipeline only exercises a handful of its branches, so pin every (prim, named)
// arm and its needsStrconv flag directly. `named` is declName != prim.
func TestFormatToString(t *testing.T) {
	cases := []struct {
		name        string
		prim, decl  string
		wantExpr    string
		wantStrconv bool
	}{
		{"string bare", "string", "string", "v", false},
		{"string named", "string", "Email", "string(v)", false},
		{"bool bare", "bool", "bool", "strconv.FormatBool(v)", true},
		{"bool named", "bool", "Flag", "strconv.FormatBool(bool(v))", true},
		{"int bare", "int", "int", "strconv.Itoa(v)", true},
		{"int named", "int", "Count", "strconv.FormatInt(int64(v), 10)", true},
		{"int8", "int8", "int8", "strconv.FormatInt(int64(v), 10)", true},
		{"int16", "int16", "int16", "strconv.FormatInt(int64(v), 10)", true},
		{"int32", "int32", "int32", "strconv.FormatInt(int64(v), 10)", true},
		{"int64 bare", "int64", "int64", "strconv.FormatInt(v, 10)", true},
		{"int64 named", "int64", "ID", "strconv.FormatInt(int64(v), 10)", true},
		{"uint bare", "uint", "uint", "strconv.FormatUint(uint64(v), 10)", true},
		{"uint32", "uint32", "uint32", "strconv.FormatUint(uint64(v), 10)", true},
		{"uint64 bare", "uint64", "uint64", "strconv.FormatUint(v, 10)", true},
		{"uint64 named", "uint64", "Big", "strconv.FormatUint(uint64(v), 10)", true},
		{"float32", "float32", "float32", "strconv.FormatFloat(float64(v), 'g', -1, 32)", true},
		{"float64 bare", "float64", "float64", "strconv.FormatFloat(v, 'g', -1, 64)", true},
		{"float64 named", "float64", "Ratio", "strconv.FormatFloat(float64(v), 'g', -1, 64)", true},
		{"unknown prim", "widget", "widget", "v", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			expr, strconv := formatToString(c.prim, c.decl, "v")
			if expr != c.wantExpr || strconv != c.wantStrconv {
				t.Errorf("formatToString(%q, %q, \"v\") = (%q, %v), want (%q, %v)",
					c.prim, c.decl, expr, strconv, c.wantExpr, c.wantStrconv)
			}
		})
	}
}
