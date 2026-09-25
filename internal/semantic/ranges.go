package semantic

import (
	"fmt"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// checkRangesAndExtras runs the field rules over every type and error body
// and the matching rules over every scalar.
func (a *analyzer) checkRangesAndExtras(files []*ast.File) {
	for _, f := range files {
		for _, d := range f.Decls {
			a.checkDeclRanges(d)
		}
	}
}

// checkDeclRanges runs the range rules for one declaration.
func (a *analyzer) checkDeclRanges(d ast.Decl) {
	switch dd := d.(type) {
	case *ast.TypeDecl:
		a.checkBodyRanges(dd.Body, dd.TypeParams)
	case *ast.ErrorDecl:
		a.checkBodyRanges(dd.Body, nil)
	case *ast.ScalarDecl:
		// Every field of the scalar's type inherits its constraints.
		a.checkValueRules(dd.Primitive, fmt.Sprintf("scalar %q", dd.Name), dd.Decorators)
	}
}

// checkBodyRanges runs the value rules and the field rules on each field of
// a body.
func (a *analyzer) checkBodyRanges(members []ast.TypeMember, typeParams []string) {
	for _, f := range ast.Fields(members) {
		a.checkValueRules(a.valuePrim(f), fmt.Sprintf("field %q", f.Name), f.Decorators)
		a.checkNullableRedundant(f)
		a.checkUniqueItemsComparable(f, typeParams)
		a.checkValueConstraintOnTypeParam(f, typeParams)
		a.checkMapKeyComparable(f, typeParams)
	}
}

// checkValueRules runs the rules on the values decs constrain, of primitive
// prim, at the field or scalar subject names.
func (a *analyzer) checkValueRules(prim, subject string, decs []*ast.Decorator) {
	a.checkPairOrdering(decs)
	a.checkBoundCapacity(prim, decs)
	a.checkIntBoundFloatLiteral(prim, subject, decs)
	a.checkNegativeOnUnsigned(prim, decs)
}

// valuePrim returns the primitive of f's values - a scalar's, else the type
// as spelled - or "" for an array or a map.
func (a *analyzer) valuePrim(f *ast.Field) string {
	if f.Type == nil || f.Type.Array {
		return ""
	}
	return a.primOf(f.Type)
}

// checkDecoratorValue checks the argument values of d, a decorator whose
// arguments fit its [Spec].
func (a *analyzer) checkDecoratorValue(d *ast.Decorator) {
	switch d.Name {
	case "pattern":
		a.checkPatternArg(d)
	case "group":
		a.checkGroupArg(d)
	case "length", "range":
		a.checkPairArgs(d)
	case "multipleOf":
		a.checkMultipleOf(d)
	case "status":
		a.checkHTTPStatus(d)
	case "timeout":
		a.checkPositiveDuration(d)
	case "maxBodySize", "maxSize":
		a.checkPositiveSize(d)
	case "minLength", "maxLength", "minItems", "maxItems":
		a.checkNonNegativeInt(d)
	}
}

// checkPairArgs requires min ≤ max in `@length` and `@range`, and
// non-negative `@length` values.
func (a *analyzer) checkPairArgs(d *ast.Decorator) {
	pos := positionalArgs(d)
	if d.Name == "length" && len(pos) == 1 {
		if l, ok := ParseNumericArg(pos[0]); ok && l.FloatVal < 0 {
			a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
				"@length: exact length must be ≥ 0 (got %g)", l.FloatVal)
		}
		return
	}
	loLit, loOk := ParseNumericArg(pos[0])
	hiLit, hiOk := ParseNumericArg(pos[1])
	if !loOk || !hiOk {
		return
	}
	lo, hi := loLit.FloatVal, hiLit.FloatVal
	if lo > hi {
		a.diag(pos[1].Pos, pos[1].Pos, lexer.SeverityError, CodeDecoratorRange,
			"@%s: min (%g) must be ≤ max (%g)", d.Name, lo, hi)
	}
	if d.Name == "length" && lo < 0 {
		a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
			"@length: min must be ≥ 0 (got %g)", lo)
	}
}

// checkMultipleOf rejects a divisor that is not positive.
func (a *analyzer) checkMultipleOf(d *ast.Decorator) {
	pos := positionalArgs(d)
	l, ok := ParseNumericArg(pos[0])
	if !ok {
		return
	}
	v := l.FloatVal
	if v == 0 {
		a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
			"@multipleOf: divisor must not be 0")
		return
	}
	if v < 0 {
		a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
			"@multipleOf: divisor must be positive (got %g)", v)
	}
}

// checkHTTPStatus rejects a `@status` code outside 100..599.
func (a *analyzer) checkHTTPStatus(d *ast.Decorator) {
	pos := positionalArgs(d)
	v, ok := pos[0].Value.(*ast.IntLit)
	if !ok {
		return
	}
	if v.Value < 100 || v.Value > 599 {
		a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
			"@status: HTTP status code must be in 100..599 (got %d)", v.Value)
	}
}

// checkPositiveDuration rejects a `@timeout` that is not positive, in bare
// seconds or as a duration, or that [DurationArg] cannot read.
func (a *analyzer) checkPositiveDuration(d *ast.Decorator) {
	pos := positionalArgs(d)
	dur, ok := DurationArg(pos[0])
	switch v := pos[0].Value.(type) {
	case *ast.DurationLit:
		switch {
		case !ok:
			a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
				"@%s: %s is not a duration (suffix must be one of %s)",
				d.Name, v.Text, strings.Join(lexer.DurationUnits, ", "))
		case dur <= 0:
			a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
				"@%s: duration must be > 0 (got %s)", d.Name, v.Text)
		}
	case *ast.IntLit:
		switch {
		case v.Value <= 0:
			a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
				"@%s: duration must be > 0 (got %d)", d.Name, v.Value)
		case !ok:
			a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
				"@%s: %d seconds is out of range for a duration (at most %d)", d.Name, v.Value, maxDurationSeconds)
		}
	}
}

// checkPositiveSize rejects a `@maxBodySize` or `@maxSize` of zero or less,
// which would mean no cap, or one [lexer.ParseSize] cannot read.
func (a *analyzer) checkPositiveSize(d *ast.Decorator) {
	pos := positionalArgs(d)
	if v, ok := pos[0].Value.(*ast.SizeLit); ok {
		if _, parsed := lexer.ParseSize(v.Text); !parsed {
			a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
				"@%s: %s is not a byte size (suffix must be one of %s, and the count must fit in an int64)",
				d.Name, v.Text, strings.Join(lexer.SizeSuffixes(), ", "))
			return
		}
	}
	if n, ok := sizeBytes(pos[0].Value); ok && n <= 0 {
		a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
			"@%s: size must be > 0 (got %d)", d.Name, n)
	}
}

// checkNonNegativeInt rejects a negative length or item count.
func (a *analyzer) checkNonNegativeInt(d *ast.Decorator) {
	pos := positionalArgs(d)
	if v, ok := pos[0].Value.(*ast.IntLit); ok && v.Value < 0 {
		a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
			"@%s: value must be ≥ 0 (got %d)", d.Name, v.Value)
	}
}

// checkPairOrdering rejects a lower bound above its upper partner on f, and
// warns when a pair with a strict bound meets at one value.
func (a *analyzer) checkPairOrdering(decs []*ast.Decorator) {
	pairs := []struct {
		lo, hi   string
		loStrict bool
		hiStrict bool
	}{
		{lo: "minLength", hi: "maxLength"},
		{lo: "minItems", hi: "maxItems"},
		{lo: "gte", hi: "lte"},
		{lo: "gt", hi: "lt", loStrict: true, hiStrict: true},
		{lo: "gte", hi: "lt", hiStrict: true},
		{lo: "gt", hi: "lte", loStrict: true},
	}
	for _, p := range pairs {
		loV, loPos, loOk := singleNumericArg(decs, p.lo)
		hiV, hiPos, hiOk := singleNumericArg(decs, p.hi)
		if !loOk || !hiOk {
			continue
		}
		if loV > hiV {
			diag := a.diag(hiPos, hiPos, lexer.SeverityError, CodeDecoratorRange,
				"@%s (%g) must be ≥ @%s (%g)", p.hi, hiV, p.lo, loV)
			diag.Related = related(loPos, "@"+p.lo+" declared here")
			continue
		}
		if loV == hiV && (p.loStrict || p.hiStrict) {
			diag := a.diag(hiPos, hiPos, lexer.SeverityWarning, CodeBoundEmptyRange,
				"@%s(%g) combined with @%s(%g) defines an empty range - no value satisfies both",
				p.hi, hiV, p.lo, loV)
			diag.Related = related(loPos, "@"+p.lo+" declared here")
		}
	}
}

// checkNullableRedundant warns about `@nullable` on an optional field.
func (a *analyzer) checkNullableRedundant(f *ast.Field) {
	var nullableDec *ast.Decorator
	for _, d := range f.Decorators {
		if d == nil {
			continue
		}
		if d.Name == "nullable" {
			nullableDec = d
		}
	}
	if nullableDec != nil && f.Type != nil && f.Type.Optional {
		a.diag(nullableDec.Pos, decoratorEnd(nullableDec),
			lexer.SeverityWarning, CodeDecoratorRedundant,
			"@nullable is redundant on optional field %q (the `?` already allows null)",
			f.Name)
	}
}

// singleNumericArg returns the first argument of the first `name` decorator
// in decs, and its position, when that argument is numeric.
func singleNumericArg(decs []*ast.Decorator, name string) (float64, lexer.Position, bool) {
	for _, d := range decs {
		if d == nil || d.Name != name {
			continue
		}
		pos := positionalArgs(d)
		if len(pos) == 0 {
			return 0, lexer.Position{}, false
		}
		l, ok := ParseNumericArg(pos[0])
		if !ok {
			return 0, lexer.Position{}, false
		}
		return l.FloatVal, pos[0].Pos, true
	}
	return 0, lexer.Position{}, false
}
