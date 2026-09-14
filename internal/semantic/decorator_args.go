// Decorator-argument literal extractors shared by the validator, OpenAPI,
// transport, and routes emitters.
package semantic

import (
	"strconv"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// This file groups the decorator-argument extractors used by every emit
// function in the validator registry. Each helper takes one
// [ast.DecoratorArg] and pulls a typed value out of it, returning the
// `(value, ok)` shape; `ok == false` is the standard signal for the
// caller to skip emitting any check (validator opts out silently).

// NumericLit is the one canonical reading of a numeric decorator-argument
// literal, shared by every constraint reader so the int/float distinction and
// the big-integer threshold are decided in a single place (instead of the
// validator, the OpenAPI bound emitter, and the big-int escape hatch each
// re-classifying the same literal and risking drift).
type NumericLit struct {
	IntVal   int64   // valid when IsInt
	FloatVal float64 // always set: float64(IntVal) for an IntLit, the value for a FloatLit
	IsInt    bool    // the literal was an integer
	IsBigInt bool    // an integer whose magnitude exceeds maxExactInt, so float64 would lose precision
}

// ParseNumericArg classifies a numeric decorator argument (IntLit / FloatLit).
// ok is false for a missing or non-numeric argument.
func ParseNumericArg(a *ast.DecoratorArg) (NumericLit, bool) {
	if a == nil || a.Value == nil {
		return NumericLit{}, false
	}
	switch v := a.Value.(type) {
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

// IntArg pulls an int64 out of a literal DecoratorArg.
func IntArg(a *ast.DecoratorArg) (int64, bool) {
	if l, ok := ParseNumericArg(a); ok && l.IsInt {
		return l.IntVal, true
	}
	return 0, false
}

// NumericArg pulls a numeric value out of a literal DecoratorArg as a
// formatted Go expression. Returns the rendered text + ok flag.
// Accepts IntLit (`@gte(0)`) and FloatLit (`@gte(0.5)`); int renders
// as `0`, float renders via `strconv.FormatFloat` with 'g' so `0.5`
// stays `0.5`, `1e3` stays `1000`, etc. Used by
// `@gt/@gte/@lt/@lte/@range/@multipleOf` - accepting only IntLit
// would silently drop float bounds even though the Spec allows
// ArgNumber.
func NumericArg(a *ast.DecoratorArg) (string, bool) {
	l, ok := ParseNumericArg(a)
	if !ok {
		return "", false
	}
	if l.IsInt {
		return strconv.FormatInt(l.IntVal, 10), true
	}
	return strconv.FormatFloat(l.FloatVal, 'g', -1, 64), true
}

// StringArg pulls a string out of a literal DecoratorArg.
func StringArg(a *ast.DecoratorArg) (string, bool) {
	if a == nil || a.Value == nil {
		return "", false
	}
	if s, ok := a.Value.(*ast.StringLit); ok {
		return s.Value, true
	}
	return "", false
}

// StringOrIdentArg returns the underlying name from either a quoted
// string literal or a bare identifier. Used by `@format(...)` where both
// forms are accepted in the DSL. Returns "" for any other expression
// kind (numbers, booleans, nested decorators).
func StringOrIdentArg(a *ast.DecoratorArg) string {
	if a == nil || a.Value == nil {
		return ""
	}
	switch v := a.Value.(type) {
	case *ast.StringLit:
		return v.Value
	case *ast.IdentExpr:
		if v.Name != nil {
			return v.Name.String()
		}
	}
	return ""
}

// StringArrayArg pulls a list of strings out of an `@mimeTypes(["a","b"])`
// argument. The literal must be an ArrayLit of StringLits - anything
// else returns nil,false so the validator skips the check.
func StringArrayArg(a *ast.DecoratorArg) ([]string, bool) {
	if a == nil || a.Value == nil {
		return nil, false
	}
	arr, ok := a.Value.(*ast.ArrayLit)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(arr.Elements))
	for _, e := range arr.Elements {
		s, ok := e.(*ast.StringLit)
		if !ok {
			return nil, false
		}
		out = append(out, s.Value)
	}
	return out, true
}

// SizeArg extracts a byte count from a Size literal (`5MB`) or a bare
// integer. Returns 0,false on any other expression kind.
func SizeArg(a *ast.DecoratorArg) (int64, bool) {
	if a == nil {
		return 0, false
	}
	return SizeBytes(a.Value)
}

// SizeBytes is [SizeArg] on a bare expression, for callers holding the
// literal without its [ast.DecoratorArg] wrapper. The suffix vocabulary and
// its multipliers live in [lexer.ParseSize].
func SizeBytes(e ast.Expr) (int64, bool) {
	switch v := e.(type) {
	case *ast.IntLit:
		return v.Value, true
	case *ast.SizeLit:
		return lexer.ParseSize(v.Text)
	}
	return 0, false
}

// maxExactInt is 2^53 - the largest magnitude an int64 keeps EXACTLY when
// converted to the float64 that JSON numbers (and openapi3.Schema.Min /
// Max) carry. Beyond it, float64(int64) rounds, so a bound like
// `@gte(9007199254740993)` or `@gte(math.MaxInt64)` would advertise a
// value the runtime validator (which keeps the exact int64) never agrees
// with - at the extreme an unsatisfiable spec.
const maxExactInt = int64(1) << 53

// StringArrayDecoratorArg returns the field-name list passed to a
// type-level decorator like `@requiresOneOf` / `@mutuallyExclusive`.
// Three argument shapes are accepted, matching the syntax the
// semantic argument-shape validator allows:
//
//   - Variadic bare idents:    @requiresOneOf(email, phone)
//   - Variadic string literals: @requiresOneOf("email", "phone")
//   - Array shortcut:           @requiresOneOf(["email", "phone"])
//
// Returns nil when the decorator has no arguments at all.
func StringArrayDecoratorArg(d *ast.Decorator) []string {
	if len(d.Args) == 0 {
		return nil
	}
	// Array shortcut: single positional that's an [ ... ] literal.
	if arr, ok := d.Args[0].Value.(*ast.ArrayLit); ok && len(d.Args) == 1 {
		return CollectStringOrIdent(arr.Elements)
	}
	// Variadic positional: each arg is its own ident or string lit.
	out := make([]string, 0, len(d.Args))
	for _, ag := range d.Args {
		if ag.Named || ag.Object != nil || ag.Nested != nil {
			continue
		}
		switch v := ag.Value.(type) {
		case *ast.StringLit:
			out = append(out, v.Value)
		case *ast.IdentExpr:
			if v.Name != nil {
				out = append(out, v.Name.String())
			}
		}
	}
	return out
}

// CollectStringOrIdent extracts every string-lit / ident-expr value
// from an [ast.ArrayLit] elements slice, skipping anything else
// silently. Other shapes are caught upstream by the
// argument-shape validator.
func CollectStringOrIdent(elems []ast.Expr) []string {
	out := make([]string, 0, len(elems))
	for _, e := range elems {
		switch v := e.(type) {
		case *ast.StringLit:
			out = append(out, v.Value)
		case *ast.IdentExpr:
			if v.Name != nil {
				out = append(out, v.Name.String())
			}
		}
	}
	return out
}
