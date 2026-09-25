package semantic

import (
	"math"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// ParseNumericArg tells ints from floats.
func TestParseNumericArg(t *testing.T) {
	mkInt := func(v int64) *ast.DecoratorArg { return &ast.DecoratorArg{Value: &ast.IntLit{Value: v}} }
	mkFloat := func(v float64) *ast.DecoratorArg { return &ast.DecoratorArg{Value: &ast.FloatLit{Value: v}} }

	if l, ok := ParseNumericArg(mkInt(10)); !ok || !l.IsInt || l.IntVal != 10 || l.FloatVal != 10 {
		t.Errorf("int 10: %+v ok=%v", l, ok)
	}
	if l, ok := ParseNumericArg(mkFloat(0.5)); !ok || l.IsInt || l.FloatVal != 0.5 {
		t.Errorf("float 0.5: %+v ok=%v", l, ok)
	}
	if _, ok := ParseNumericArg(nil); ok {
		t.Error("nil arg must be !ok")
	}
	if _, ok := ParseNumericArg(&ast.DecoratorArg{Value: &ast.StringLit{Value: "x"}}); ok {
		t.Error("non-numeric arg must be !ok")
	}

	// IntArg reads through it.
	if v, ok := IntArg(mkInt(42)); !ok || v != 42 {
		t.Errorf("IntArg(42) = %d,%v", v, ok)
	}
	if _, ok := IntArg(mkFloat(1.5)); ok {
		t.Error("IntArg must reject a float")
	}
}

// NumericLit tells whole values from fractional ones and renders each.
func TestNumericLitForms(t *testing.T) {
	for _, c := range []struct {
		e            ast.Expr
		whole        bool
		text, wholeT string
	}{
		{&ast.IntLit{Value: -7}, true, "-7", "-7"},
		{&ast.FloatLit{Value: 300}, true, "300", "300"},
		{&ast.FloatLit{Value: 1e19}, true, "1e+19", "10000000000000000000"},
		{&ast.FloatLit{Value: 0.5}, false, "0.5", ""},
		{&ast.FloatLit{Value: math.Inf(1)}, false, "+Inf", ""},
		// The written text is exact where float64 rounds.
		{&ast.FloatLit{Value: 18446744073709551615.0, Text: "18446744073709551615.0"}, true, "1.8446744073709552e+19", "18446744073709551615"},
		{&ast.FloatLit{Value: 1, Text: "1.00000000000000000001"}, false, "1", ""},
	} {
		l, ok := ParseNumeric(c.e)
		if !ok {
			t.Fatalf("ParseNumeric(%#v) not numeric", c.e)
		}
		wt, whole := l.WholeText()
		if l.IsWhole() != c.whole || whole != c.whole || l.Text() != c.text || wt != c.wholeT {
			t.Errorf("%#v: IsWhole=%v Text=%q WholeText=%q,%v; want %v %q %q", c.e, l.IsWhole(), l.Text(), wt, whole, c.whole, c.text, c.wholeT)
		}
	}
	if _, ok := ParseNumeric(&ast.StringLit{}); ok {
		t.Error("a string is not numeric")
	}
}
