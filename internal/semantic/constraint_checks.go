package semantic

import (
	"math"
	"slices"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/prims"
)

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

// checkNegativeOnUnsigned rejects `@negative` and `@lt(0)` on an unsigned
// prim, since no value satisfies them.
func (a *analyzer) checkNegativeOnUnsigned(prim string, decs []*ast.Decorator) {
	if !prims.IsUnsigned(prim) {
		return
	}
	for _, d := range decs {
		switch {
		case d.Name == "negative":
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
				"@negative cannot apply to an unsigned type (%s is always >= 0) - every value would be rejected; use a signed integer or drop @negative", prim)
		case d.Name == "lt" && len(d.Args) == 1:
			// 0 is in range, so the capacity check passes `@lt(0)`.
			if l, ok := ParseNumericArg(d.Args[0]); ok && l.FloatVal == 0 {
				a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
					"@lt(0) cannot apply to an unsigned type (%s is always >= 0) - every value would be rejected; use a signed integer or a positive bound", prim)
			}
		}
	}
}

// checkBoundCapacity rejects a numeric constraint argument in decs that
// prim cannot hold, as a constant that would not compile.
func (a *analyzer) checkBoundCapacity(prim string, decs []*ast.Decorator) {
	for _, d := range decs {
		for _, arg := range numericArgs(d) {
			if l, ok := ParseNumericArg(arg); ok {
				a.checkCapacity(prim, l, arg.Pos, "@"+d.Name+" bound")
			}
		}
	}
}

// checkCapacity reports literal l, written at pos as what, when prim cannot
// hold it: a whole value outside an integer primitive's range, or a value
// beyond float32's.
func (a *analyzer) checkCapacity(prim string, l NumericLit, pos lexer.Position, what string) {
	if lo, hi, ok := prims.Capacity(prim); ok {
		if text, whole := l.WholeText(); whole && (l.FloatVal < lo || l.FloatVal > hi) {
			a.diag(pos, pos, lexer.SeverityError, CodeBoundOverflow,
				"%s %s exceeds %s range [%g, %g]", what, text, prim, lo, hi)
		}
		return
	}
	if prim == "float32" && math.Abs(l.FloatVal) > math.MaxFloat32 {
		a.diag(pos, pos, lexer.SeverityError, CodeBoundOverflow,
			"%s %s exceeds float32 range [%g, %g]", what, l.Text(), -math.MaxFloat32, math.MaxFloat32)
	}
}

// numericArgs returns the arguments of numeric constraint d, when their
// count fits its [Spec]: the bound of @gt, @gte, @lt and @lte, the ends of
// @range, the divisor of @multipleOf; nil for any other decorator.
func numericArgs(d *ast.Decorator) []*ast.DecoratorArg {
	spec, ok := DecoratorSpec(d.Name)
	if !ok || spec.Constraint != ConstraintNumeric {
		return nil
	}
	args := positionalArgs(d)
	if len(args) < spec.Args.Min || (spec.Args.Max >= 0 && len(args) > spec.Args.Max) {
		return nil
	}
	return args
}

// checkIntBoundFloatLiteral rejects a fractional argument of a numeric
// constraint on an integer target; a whole float such as 1000.0 passes.
func (a *analyzer) checkIntBoundFloatLiteral(prim, target string, decs []*ast.Decorator) {
	if !prims.IsInteger(prim) {
		return
	}
	for _, d := range decs {
		for _, arg := range numericArgs(d) {
			if l, ok := ParseNumericArg(arg); ok && !l.IsWhole() {
				a.diag(arg.Pos, arg.Pos, lexer.SeverityError, CodeDecoratorTypeMismatch,
					"@%s argument %g must be a whole number on integer %s: the generated check compares or divides an integer by it",
					d.Name, l.FloatVal, target)
			}
		}
	}
}

// checkValueConstraintOnTypeParam rejects a numeric or text constraint on a
// bare type-parameter field, which the generic validator sees as `any`.
func (a *analyzer) checkValueConstraintOnTypeParam(f *ast.Field, typeParams []string) {
	if f == nil || f.Type == nil || f.Type.Array || f.Type.Map != nil || f.Type.Named == nil {
		return
	}
	name := f.Type.Named.Name.String()
	if !slices.Contains(typeParams, name) {
		return
	}
	for _, d := range f.Decorators {
		if ConstraintOf(d.Name)&ConstraintNarrowing != 0 {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
				"@%s cannot constrain a type-parameter field (%s): the parametric validator sees it as `any` and can't enforce the bound, while the monomorphised OpenAPI would still advertise it. Drop the decorator, or constrain a concrete type the instance supplies.",
				d.Name, name)
			return
		}
	}
}

// checkBoundOverlap warns when `@length` or `@range` shares a field with one
// of its one-sided forms.
func (a *analyzer) checkBoundOverlap(parent string, f *ast.Field) {
	if f == nil {
		return
	}
	for _, d := range f.Decorators {
		if d == nil {
			continue
		}
		var partners []string
		switch d.Name {
		case "length":
			partners = []string{"minLength", "maxLength"}
		case "range":
			partners = []string{"gt", "gte", "lt", "lte"}
		default:
			continue
		}
		for _, p := range f.Decorators {
			if p == nil || p == d {
				continue
			}
			for _, want := range partners {
				if p.Name != want {
					continue
				}
				a.diag(p.Pos, decoratorEnd(p), lexer.SeverityWarning, CodeDecoratorRedundant,
					"field %s.%s: @%s overlaps with @%s on the same field; pick one form for clarity",
					parent, f.Name, p.Name, d.Name)
			}
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
