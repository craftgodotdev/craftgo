package semantic

import (
	"math"
	"math/big"
	"strconv"
	"time"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// NumericLit is a numeric literal, read as an integer or a float.
type NumericLit struct {
	IntVal   int64   // valid when IsInt
	FloatVal float64 // always set: float64(IntVal) for an IntLit, the value for a FloatLit
	IsInt    bool    // the literal was an integer
	IsBigInt bool    // an integer whose magnitude exceeds maxExactInt, so float64 would lose precision
}

// ParseNumeric classifies e, an int or float literal; ok is false for any
// other expression.
func ParseNumeric(e ast.Expr) (NumericLit, bool) {
	switch v := e.(type) {
	case *ast.IntLit:
		return NumericLit{
			IntVal:   v.Value,
			FloatVal: float64(v.Value),
			IsInt:    true,
			IsBigInt: v.Value > maxExactInt || v.Value < -maxExactInt,
		}, true
	case *ast.FloatLit:
		return NumericLit{FloatVal: v.Value}, true
	}
	return NumericLit{}, false
}

// ParseNumericArg is [ParseNumeric] on a decorator argument; ok is false
// for a missing one.
func ParseNumericArg(a *ast.DecoratorArg) (NumericLit, bool) {
	if a == nil {
		return NumericLit{}, false
	}
	return ParseNumeric(a.Value)
}

// IsWhole reports whether l is a finite whole number: an integer, or a
// float with no fractional part such as 300.0.
func (l NumericLit) IsWhole() bool {
	return l.IsInt || (!math.IsInf(l.FloatVal, 0) && l.FloatVal == math.Trunc(l.FloatVal))
}

// Text renders l as Go literal text: an integer in decimal, a float in its
// shortest 'g' form.
func (l NumericLit) Text() string {
	if l.IsInt {
		return strconv.FormatInt(l.IntVal, 10)
	}
	return strconv.FormatFloat(l.FloatVal, 'g', -1, 64)
}

// WholeText renders a whole l in decimal digits, a float as the exact
// integer it holds; ok is false when l is not [NumericLit.IsWhole].
func (l NumericLit) WholeText() (string, bool) {
	if l.IsInt {
		return strconv.FormatInt(l.IntVal, 10), true
	}
	if !l.IsWhole() {
		return "", false
	}
	n, _ := new(big.Float).SetFloat64(l.FloatVal).Int(nil)
	return n.String(), true
}

// IntArg pulls an int64 out of a literal DecoratorArg.
func IntArg(a *ast.DecoratorArg) (int64, bool) {
	if l, ok := ParseNumericArg(a); ok && l.IsInt {
		return l.IntVal, true
	}
	return 0, false
}

// NumericArg renders a numeric argument as [NumericLit.Text].
func NumericArg(a *ast.DecoratorArg) (string, bool) {
	l, ok := ParseNumericArg(a)
	if !ok {
		return "", false
	}
	return l.Text(), true
}

// SizeArg extracts a byte count from a Size literal (`5MB`) or a bare
// integer. Returns 0,false on any other expression kind.
func SizeArg(a *ast.DecoratorArg) (int64, bool) {
	if a == nil {
		return 0, false
	}
	return sizeBytes(a.Value)
}

// DurationArg extracts a duration from a Duration literal (`30s`) or a bare
// integer, which counts seconds. Returns 0,false on any other expression
// kind, an unreadable literal, or seconds past a [time.Duration].
func DurationArg(a *ast.DecoratorArg) (time.Duration, bool) {
	if a == nil {
		return 0, false
	}
	switch v := a.Value.(type) {
	case *ast.IntLit:
		if v.Value > maxDurationSeconds || v.Value < -maxDurationSeconds {
			return 0, false
		}
		return time.Duration(v.Value) * time.Second, true
	case *ast.DurationLit:
		return lexer.ParseDuration(v.Text)
	}
	return 0, false
}

// sizeBytes is [SizeArg] on a bare expression.
func sizeBytes(e ast.Expr) (int64, bool) {
	switch v := e.(type) {
	case *ast.IntLit:
		return v.Value, true
	case *ast.SizeLit:
		return lexer.ParseSize(v.Text)
	}
	return 0, false
}

// maxDurationSeconds is the most whole seconds a [time.Duration] holds.
const maxDurationSeconds = math.MaxInt64 / int64(time.Second)

// maxExactInt is 2^53, the magnitude up to which a float64 - and so a JSON
// number - holds every integer exactly.
const maxExactInt = int64(1) << 53
