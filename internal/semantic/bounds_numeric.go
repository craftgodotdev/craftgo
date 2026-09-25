package semantic

import (
	"fmt"
	"math"
	"slices"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/prims"
)

// checkNegativeOnUnsigned rejects `@negative` and `@lt(0)` on an unsigned
// field, scalars included, since no value satisfies them.
func (a *analyzer) checkNegativeOnUnsigned(f *ast.Field) {
	if f == nil || f.Type == nil || f.Type.Named == nil {
		return
	}
	prim := a.primOf(f.Type)
	if !prims.IsUnsigned(prim) {
		return
	}
	for _, d := range f.Decorators {
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

// checkBoundCapacity rejects a numeric constraint argument f's integer or
// float32 type cannot hold, as a constant that would not compile.
func (a *analyzer) checkBoundCapacity(f *ast.Field) {
	if f == nil || f.Type == nil || f.Type.Array || f.Type.Named == nil {
		return
	}
	prim := a.primOf(f.Type)
	for _, d := range f.Decorators {
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
	spec, ok := Lookup(d.Name)
	if !ok || spec.Constraint != ConstraintNumeric {
		return nil
	}
	args := positionalArgs(d)
	if len(args) < spec.Args.Min || (spec.Args.Max >= 0 && len(args) > spec.Args.Max) {
		return nil
	}
	return args
}

// checkBoundLiteralKind rejects a fractional numeric constraint argument on
// an integer field.
func (a *analyzer) checkBoundLiteralKind(f *ast.Field) {
	if f == nil || f.Type == nil || f.Type.Array || f.Type.Named == nil {
		return
	}
	prim := a.primOf(f.Type)
	a.checkIntBoundFloatLiteral(prim, fmt.Sprintf("field %q", f.Name), f.Decorators)
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
