package semantic

import (
	"strconv"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// NumericLit is a numeric decorator argument, read as an integer or a float.
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

// NumericArg renders a numeric argument as Go literal text: an integer in
// decimal, a float in its shortest 'g' form.
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

// StringOrIdentArg returns the text of a string literal or bare identifier
// argument, or "" for anything else.
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

// StringArrayArg returns the elements of an array-literal argument, or false
// unless every element is a string literal.
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

// SizeBytes is [SizeArg] on a bare expression.
func SizeBytes(e ast.Expr) (int64, bool) {
	switch v := e.(type) {
	case *ast.IntLit:
		return v.Value, true
	case *ast.SizeLit:
		return lexer.ParseSize(v.Text)
	}
	return 0, false
}

// maxExactInt is 2^53, the magnitude up to which a float64 - and so a JSON
// number - holds every integer exactly.
const maxExactInt = int64(1) << 53

// StringArrayDecoratorArg returns the names a list decorator such as
// `@requiresOneOf` carries, as identifiers, strings or one array literal.
func StringArrayDecoratorArg(d *ast.Decorator) []string {
	if len(d.Args) == 0 {
		return nil
	}
	if arr, ok := d.Args[0].Value.(*ast.ArrayLit); ok && len(d.Args) == 1 {
		return CollectStringOrIdent(arr.Elements)
	}
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

// CollectStringOrIdent returns the text of every string literal and
// identifier in elems, skipping anything else.
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
