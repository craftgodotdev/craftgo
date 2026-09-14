package semantic

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// TestParseNumericArg pins the single classifier every constraint reader
// (IntArg, NumericArg, numericArgValue, rawIfBigInt) now derives from, so the
// int/float distinction and the big-integer threshold are decided once.
func TestParseNumericArg(t *testing.T) {
	mkInt := func(v int64) *ast.DecoratorArg { return &ast.DecoratorArg{Value: &ast.IntLit{Value: v}} }
	mkFloat := func(v float64) *ast.DecoratorArg { return &ast.DecoratorArg{Value: &ast.FloatLit{Value: v}} }

	if l, ok := ParseNumericArg(mkInt(10)); !ok || !l.IsInt || l.IntVal != 10 || l.FloatVal != 10 || l.IsBigInt {
		t.Errorf("int 10: %+v ok=%v", l, ok)
	}
	// The threshold is strict: 2^53 stays exact (not big), 2^53+1 is big.
	if l, _ := ParseNumericArg(mkInt(maxExactInt)); l.IsBigInt {
		t.Errorf("2^53 must not be flagged big (threshold is strict >)")
	}
	if l, ok := ParseNumericArg(mkInt(maxExactInt + 1)); !ok || !l.IsInt || !l.IsBigInt || l.IntVal != maxExactInt+1 {
		t.Errorf("2^53+1: %+v ok=%v", l, ok)
	}
	if l, ok := ParseNumericArg(mkInt(-(maxExactInt + 1))); !ok || !l.IsBigInt {
		t.Errorf("negative big int: %+v ok=%v", l, ok)
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

	// The four readers all agree through the classifier.
	if v, ok := IntArg(mkInt(42)); !ok || v != 42 {
		t.Errorf("IntArg(42) = %d,%v", v, ok)
	}
	if _, ok := IntArg(mkFloat(1.5)); ok {
		t.Error("IntArg must reject a float")
	}
	if s, ok := NumericArg(mkFloat(0.5)); !ok || s != "0.5" {
		t.Errorf("NumericArg(0.5) = %q,%v", s, ok)
	}
}
