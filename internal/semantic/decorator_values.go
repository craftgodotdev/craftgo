package semantic

import (
	"math"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// IsDeprecated reports whether the decorators mark the construct
// deprecated.
func IsDeprecated(ds []*ast.Decorator) bool {
	return ast.HasDecorator(ds, "deprecated")
}

// DeprecatedReason returns the optional `@deprecated("...")` message, or
// "" when the decorator is absent or carries no argument.
func DeprecatedReason(ds []*ast.Decorator) string {
	reason, _ := ast.StringArg(ds, "deprecated")
	return reason
}

// CrossFieldNames returns the fields an `@requiresOneOf` or
// `@mutuallyExclusive` lists, each once, in order.
func CrossFieldNames(d *ast.Decorator) []string {
	var out []string
	for _, n := range ast.ArgNames(d) {
		if !slices.Contains(out, n.Value) {
			out = append(out, n.Value)
		}
	}
	return out
}

// Description returns a node's `@doc` text, else its leading comment block.
func Description(decs []*ast.Decorator, doc []string) string {
	if s, _ := ast.StringArg(decs, "doc"); s != "" {
		return s
	}
	return strings.Join(doc, "\n")
}

// descriptionLines is [Description] split at the author's line breaks.
func descriptionLines(decs []*ast.Decorator, doc []string) []string {
	desc := Description(decs, doc)
	if desc == "" {
		return nil
	}
	return strings.Split(desc, "\n")
}

// FieldIsRequired reports whether f must be present: it is neither optional
// nor defaulted.
func FieldIsRequired(f *ast.Field) bool {
	if f == nil || f.Type == nil || f.Type.Optional {
		return false
	}
	return !ast.HasDecorator(f.Decorators, "default")
}

// ResolveDefaultValue returns the literal of f's `@default`, with enum
// member names, alone or in an array, resolved to their wire values.
func ResolveDefaultValue(f *ast.Field, pkg *Package) (any, bool) {
	return resolveDecoratorLiteral(f, pkg, "default")
}

// ExampleValue is [ResolveDefaultValue] for `@example`.
func ExampleValue(f *ast.Field, pkg *Package) (any, bool) {
	return resolveDecoratorLiteral(f, pkg, "example")
}

// resolveDecoratorLiteral returns the literal of f's decName decorator, with
// enum member names, alone or in an array, resolved to their wire values.
func resolveDecoratorLiteral(f *ast.Field, pkg *Package, decName string) (any, bool) {
	if f == nil {
		return nil, false
	}
	for _, d := range f.Decorators {
		if d.Name != decName || len(d.Args) == 0 {
			continue
		}
		// An array field's element type names the enum too.
		enumName := ""
		if f.Type != nil && f.Type.Named != nil && f.Type.Named.Name != nil {
			enumName = f.Type.Named.Name.String()
		}
		return literalValue(d.Args[0].Value, func(name string) any {
			if wire, ok := resolveEnumMember(pkg, enumName, name); ok {
				return wire
			}
			return name
		})
	}
	return nil, false
}

// literalValue converts literal e, arrays included, to its Go value, with
// ident giving an identifier's; ok is false for any other expression.
func literalValue(e ast.Expr, ident func(name string) any) (any, bool) {
	switch v := e.(type) {
	case *ast.StringLit:
		return v.Value, true
	case *ast.IntLit:
		return v.Value, true
	case *ast.FloatLit:
		return v.Value, true
	case *ast.BoolLit:
		return v.Value, true
	case *ast.NullLit:
		return nil, true
	case *ast.IdentExpr:
		if v.Name == nil {
			return nil, false
		}
		return ident(v.Name.String()), true
	case *ast.ArrayLit:
		out := make([]any, 0, len(v.Elements))
		for _, el := range v.Elements {
			x, ok := literalValue(el, ident)
			if !ok {
				return nil, false
			}
			out = append(out, x)
		}
		return out, true
	}
	return nil, false
}

// resolveEnumMember returns the wire value of member in enum enumName, or
// false when pkg has no such enum or member.
func resolveEnumMember(pkg *Package, enumName, member string) (any, bool) {
	if pkg == nil || enumName == "" {
		return nil, false
	}
	ed, ok := pkg.Enums[enumName]
	if !ok {
		return nil, false
	}
	if ev := enumMember(ed, member); ev != nil {
		return EnumMemberWire(ev), true
	}
	return nil, false
}

// NumericLit is a numeric literal, read as an integer or a float.
type NumericLit struct {
	IntVal   int64   // valid when IsInt
	FloatVal float64 // always set: float64(IntVal) for an IntLit, the value for a FloatLit
	IsInt    bool    // the literal was an integer
	written  string  // a FloatLit as written, whose exact value FloatVal may round
}

// ParseNumeric classifies e, an int or float literal; ok is false for any
// other expression.
func ParseNumeric(e ast.Expr) (NumericLit, bool) {
	switch v := e.(type) {
	case *ast.IntLit:
		return NumericLit{IntVal: v.Value, FloatVal: float64(v.Value), IsInt: true}, true
	case *ast.FloatLit:
		return NumericLit{FloatVal: v.Value, written: v.Text}, true
	}
	return NumericLit{}, false
}

// Rat returns l's exact value: a float's digits as written, not the nearest
// float64, which past 2^53 skips integers; nil for a non-finite float without text.
func (l NumericLit) Rat() *big.Rat {
	r := new(big.Rat)
	if l.IsInt {
		return r.SetInt64(l.IntVal)
	}
	if _, ok := r.SetString(l.written); ok {
		return r
	}
	return r.SetFloat64(l.FloatVal)
}

// Cmp compares the exact values of l and m as [big.Rat.Cmp] does.
func (l NumericLit) Cmp(m NumericLit) int {
	return l.Rat().Cmp(m.Rat())
}

// ParseNumericArg is [ParseNumeric] on a decorator argument; ok is false
// for a missing one.
func ParseNumericArg(a *ast.DecoratorArg) (NumericLit, bool) {
	if a == nil {
		return NumericLit{}, false
	}
	return ParseNumeric(a.Value)
}

// IsWhole reports whether l is a whole number as written: an integer, or a
// float with no fractional part such as 300.0.
func (l NumericLit) IsWhole() bool {
	_, ok := l.wholeInt()
	return ok
}

// wholeInt returns l's value as written as an exact integer; ok is false when
// l is not a whole number.
func (l NumericLit) wholeInt() (*big.Int, bool) {
	if r := l.Rat(); r != nil && r.IsInt() {
		return r.Num(), true
	}
	return nil, false
}

// Text renders l as Go literal text: an integer in decimal, a float in its
// shortest 'g' form.
func (l NumericLit) Text() string {
	if l.IsInt {
		return strconv.FormatInt(l.IntVal, 10)
	}
	return strconv.FormatFloat(l.FloatVal, 'g', -1, 64)
}

// WholeText renders a whole l in decimal digits, a float as the exact integer
// it writes; ok is false when l is not [NumericLit.IsWhole].
func (l NumericLit) WholeText() (string, bool) {
	n, ok := l.wholeInt()
	if !ok {
		return "", false
	}
	return n.String(), true
}

// IntArg pulls an int64 out of a literal DecoratorArg.
func IntArg(a *ast.DecoratorArg) (int64, bool) {
	if l, ok := ParseNumericArg(a); ok && l.IsInt {
		return l.IntVal, true
	}
	return 0, false
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
