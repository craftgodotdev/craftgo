package semantic

import (
	"fmt"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/prims"
)

// checkRangesAndExtras runs the decorator value rules and the field rules
// over every declaration.
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
	case *ast.EventDecl:
		a.checkDecoratorRanges(dd.Decorators)
	case *ast.ScalarDecl:
		a.checkDecoratorRanges(dd.Decorators)
		// Every field of the scalar's type inherits its constraints, so the
		// field rules run here on a field of the scalar's primitive.
		a.checkIntBoundFloatLiteral(dd.Primitive, fmt.Sprintf("scalar %q", dd.Name), dd.Decorators)
		scalarAsField := &ast.Field{
			Name:       dd.Name,
			Type:       &ast.TypeRef{Named: &ast.NamedTypeRef{Pos: dd.Pos, Name: &ast.QualifiedIdent{Pos: dd.Pos, Parts: []string{dd.Primitive}}}},
			Decorators: dd.Decorators,
		}
		a.checkBoundCapacity(scalarAsField)
		a.checkNegativeOnUnsigned(scalarAsField)
		a.checkPairOrdering(scalarAsField)
		if dd.Primitive == "bytes" && !HasRawFormat(dd.Decorators) {
			for _, d := range dd.Decorators {
				if d != nil && (d.Name == "pattern" || d.Name == "format") {
					a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
						"@%s applies to text, not a `bytes` scalar - a binary value has no string pattern / format. Use a `string` scalar, or drop the decorator.",
						d.Name)
				}
			}
		}
		isFloat := dd.Primitive == "float32" || dd.Primitive == "float64"
		for _, d := range dd.Decorators {
			if d == nil || d.Name != "multipleOf" {
				continue
			}
			if isFloat {
				a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
					"@multipleOf does not support float scalars - Go's modulus operator is integer-only. Use an integer scalar, or add a tolerance check in your handler.")
				continue
			}
			if prims.IsInteger(dd.Primitive) && len(d.Args) == 1 {
				if fl, ok := d.Args[0].Value.(*ast.FloatLit); ok && !isIntegralFloat(fl.Value) {
					a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
						"@multipleOf on an integer scalar needs a whole-number divisor - Go's modulus is integer-only, so a fractional divisor can't be enforced (the OpenAPI would advertise a bound the validator drops). Use a whole number.")
				}
			}
		}
	case *ast.ServiceDecl:
		for _, m := range dd.Methods() {
			a.checkDecoratorRanges(m.Decorators)
		}
	}
}

// checkBodyRanges runs the decorator value rules and the field rules on each
// field of a body.
func (a *analyzer) checkBodyRanges(members []ast.TypeMember, typeParams []string) {
	for _, m := range members {
		f, ok := m.(*ast.Field)
		if !ok {
			continue
		}
		a.checkDecoratorRanges(f.Decorators)
		a.checkPairOrdering(f)
		a.checkNullableRedundant(f)
		a.checkBoundCapacity(f)
		a.checkBoundLiteralKind(f)
		a.checkMultipleOfTarget(f)
		a.checkNegativeOnUnsigned(f)
		a.checkUniqueItemsComparable(f, typeParams)
		a.checkValueConstraintOnTypeParam(f, typeParams)
		a.checkPatternFormatOnBytes(f)
		a.checkMapKeyComparable(f, typeParams)
	}
}

// checkDecoratorRanges checks the argument values of each decorator in decs.
func (a *analyzer) checkDecoratorRanges(decs []*ast.Decorator) {
	for _, d := range decs {
		if d == nil {
			continue
		}
		switch d.Name {
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
}

// checkPairArgs requires min ≤ max in `@length` and `@range`, and
// non-negative `@length` values.
func (a *analyzer) checkPairArgs(d *ast.Decorator) {
	pos := positionalArgs(d)
	if d.Name == "length" && len(pos) == 1 {
		if v, ok := numericValue(pos[0].Value); ok && v < 0 {
			a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
				"@length: exact length must be ≥ 0 (got %g)", v)
		}
		return
	}
	if len(pos) != 2 {
		return
	}
	lo, loOk := numericValue(pos[0].Value)
	hi, hiOk := numericValue(pos[1].Value)
	if !loOk || !hiOk {
		return
	}
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
	if len(pos) != 1 {
		return
	}
	v, ok := numericValue(pos[0].Value)
	if !ok {
		return
	}
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
	if len(pos) != 1 {
		return
	}
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
// seconds or as a duration, or that [lexer.ParseDuration] cannot read.
func (a *analyzer) checkPositiveDuration(d *ast.Decorator) {
	pos := positionalArgs(d)
	if len(pos) != 1 {
		return
	}
	if v, ok := pos[0].Value.(*ast.DurationLit); ok {
		dur, parsed := lexer.ParseDuration(v.Text)
		switch {
		case !parsed:
			a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
				"@%s: %s is not a duration (suffix must be one of %s)",
				d.Name, v.Text, strings.Join(lexer.DurationUnits, ", "))
		case dur <= 0:
			a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
				"@%s: duration must be > 0 (got %s)", d.Name, v.Text)
		}
		return
	}
	if v, ok := pos[0].Value.(*ast.IntLit); ok && v.Value <= 0 {
		a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
			"@%s: duration must be > 0 (got %d)", d.Name, v.Value)
	}
}

// checkPositiveSize rejects a `@maxBodySize` or `@maxSize` of zero or less,
// which would mean no cap, or one [lexer.ParseSize] cannot read.
func (a *analyzer) checkPositiveSize(d *ast.Decorator) {
	pos := positionalArgs(d)
	if len(pos) != 1 {
		return
	}
	if v, ok := pos[0].Value.(*ast.SizeLit); ok {
		if _, parsed := lexer.ParseSize(v.Text); !parsed {
			a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
				"@%s: %s is not a byte size (suffix must be one of %s, and the count must fit in an int64)",
				d.Name, v.Text, strings.Join(lexer.SizeSuffixes(), ", "))
			return
		}
	}
	if n, ok := SizeBytes(pos[0].Value); ok && n <= 0 {
		a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
			"@%s: size must be > 0 (got %d)", d.Name, n)
	}
}

// checkNonNegativeInt rejects a negative length or item count.
func (a *analyzer) checkNonNegativeInt(d *ast.Decorator) {
	pos := positionalArgs(d)
	if len(pos) != 1 {
		return
	}
	if v, ok := pos[0].Value.(*ast.IntLit); ok && v.Value < 0 {
		a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
			"@%s: value must be ≥ 0 (got %d)", d.Name, v.Value)
	}
}

// checkPairOrdering rejects a lower bound above its upper partner on f, and
// warns when a pair with a strict bound meets at one value.
func (a *analyzer) checkPairOrdering(f *ast.Field) {
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
		loV, loPos, loOk := singleNumericArg(f.Decorators, p.lo)
		hiV, hiPos, hiOk := singleNumericArg(f.Decorators, p.hi)
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
		v, ok := numericValue(pos[0].Value)
		if !ok {
			return 0, lexer.Position{}, false
		}
		return v, pos[0].Pos, true
	}
	return 0, lexer.Position{}, false
}

// numericValue returns an int or float literal's value as a float64.
func numericValue(e ast.Expr) (float64, bool) {
	switch v := e.(type) {
	case *ast.IntLit:
		return float64(v.Value), true
	case *ast.FloatLit:
		return v.Value, true
	}
	return 0, false
}
