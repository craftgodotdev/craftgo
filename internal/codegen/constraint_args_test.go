package codegen

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// TestParseNumericArg pins the single classifier every constraint reader
// (intArg, numericArg, numericArgValue, rawIfBigInt) now derives from, so the
// int/float distinction and the big-integer threshold are decided once.
func TestParseNumericArg(t *testing.T) {
	mkInt := func(v int64) *ast.DecoratorArg { return &ast.DecoratorArg{Value: &ast.IntLit{Value: v}} }
	mkFloat := func(v float64) *ast.DecoratorArg { return &ast.DecoratorArg{Value: &ast.FloatLit{Value: v}} }

	if l, ok := parseNumericArg(mkInt(10)); !ok || !l.isInt || l.intVal != 10 || l.floatVal != 10 || l.isBigInt {
		t.Errorf("int 10: %+v ok=%v", l, ok)
	}
	// The threshold is strict: 2^53 stays exact (not big), 2^53+1 is big.
	if l, _ := parseNumericArg(mkInt(maxExactInt)); l.isBigInt {
		t.Errorf("2^53 must not be flagged big (threshold is strict >)")
	}
	if l, ok := parseNumericArg(mkInt(maxExactInt + 1)); !ok || !l.isInt || !l.isBigInt || l.intVal != maxExactInt+1 {
		t.Errorf("2^53+1: %+v ok=%v", l, ok)
	}
	if l, ok := parseNumericArg(mkInt(-(maxExactInt + 1))); !ok || !l.isBigInt {
		t.Errorf("negative big int: %+v ok=%v", l, ok)
	}
	if l, ok := parseNumericArg(mkFloat(0.5)); !ok || l.isInt || l.floatVal != 0.5 {
		t.Errorf("float 0.5: %+v ok=%v", l, ok)
	}
	if _, ok := parseNumericArg(nil); ok {
		t.Error("nil arg must be !ok")
	}
	if _, ok := parseNumericArg(&ast.DecoratorArg{Value: &ast.StringLit{Value: "x"}}); ok {
		t.Error("non-numeric arg must be !ok")
	}

	// The four readers all agree through the classifier.
	if v, ok := intArg(mkInt(42)); !ok || v != 42 {
		t.Errorf("intArg(42) = %d,%v", v, ok)
	}
	if _, ok := intArg(mkFloat(1.5)); ok {
		t.Error("intArg must reject a float")
	}
	if s, ok := numericArg(mkFloat(0.5)); !ok || s != "0.5" {
		t.Errorf("numericArg(0.5) = %q,%v", s, ok)
	}
}
