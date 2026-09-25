package semantic

import (
	"fmt"
	"math"
	"slices"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/prims"
)

// checkMultipleOfTarget rejects `@multipleOf` on a float field, and a
// fractional divisor on an integer one.
func (a *analyzer) checkMultipleOfTarget(f *ast.Field) {
	if f == nil || f.Type == nil || f.Type.Named == nil {
		return
	}
	prim := a.primOf(f.Type)
	isFloat := prim == "float32" || prim == "float64"
	for _, d := range f.Decorators {
		if d == nil || d.Name != "multipleOf" {
			continue
		}
		if isFloat {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
				"@multipleOf does not support float fields - Go's modulus operator is integer-only. Move the field to an integer type or add a tolerance check in your handler.")
			continue
		}
		if len(d.Args) == 1 {
			if l, ok := ParseNumericArg(d.Args[0]); ok && !l.IsWhole() {
				a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
					"@multipleOf on an integer field needs a whole-number divisor - Go's modulus is integer-only, so a fractional divisor can't be enforced (the OpenAPI would advertise a bound the validator drops). Use a whole number.")
			}
		}
	}
}

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
		case d == nil:
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

// checkBoundCapacity rejects a numeric bound f's integer or float32 type
// cannot hold, as a constant that would not compile.
func (a *analyzer) checkBoundCapacity(f *ast.Field) {
	if f == nil || f.Type == nil || f.Type.Array || f.Type.Named == nil {
		return
	}
	prim := a.primOf(f.Type)
	forEachNumericBound(f, func(d *ast.Decorator, arg *ast.DecoratorArg) {
		if l, ok := ParseNumericArg(arg); ok {
			a.checkCapacity(prim, l, arg.Pos, "@"+d.Name+" bound")
		}
	})
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

// forEachNumericBound calls check on every numeric bound argument of f, both
// `@range` endpoints included.
func forEachNumericBound(f *ast.Field, check func(d *ast.Decorator, arg *ast.DecoratorArg)) {
	for _, d := range f.Decorators {
		if d == nil {
			continue
		}
		switch d.Name {
		case "gt", "gte", "lt", "lte", "multipleOf":
			if len(d.Args) == 1 {
				check(d, d.Args[0])
			}
		case "range":
			for _, ag := range d.Args {
				check(d, ag)
			}
		}
	}
}

// checkBoundLiteralKind rejects a fractional bound on an integer field.
func (a *analyzer) checkBoundLiteralKind(f *ast.Field) {
	if f == nil || f.Type == nil || f.Type.Array || f.Type.Named == nil {
		return
	}
	prim := a.primOf(f.Type)
	a.checkIntBoundFloatLiteral(prim, fmt.Sprintf("field %q", f.Name), f.Decorators)
}

// checkIntBoundFloatLiteral rejects a fractional `@gt`, `@gte`, `@lt`, `@lte`
// or `@range` bound on an integer target; a whole float such as 1000.0 passes.
func (a *analyzer) checkIntBoundFloatLiteral(prim, target string, decs []*ast.Decorator) {
	if _, _, ok := prims.Capacity(prim); !ok {
		return // not an integer primitive - float bounds are valid
	}
	for _, d := range decs {
		if d == nil {
			continue
		}
		var args []*ast.DecoratorArg
		switch d.Name {
		case "gt", "gte", "lt", "lte":
			if len(d.Args) == 1 {
				args = d.Args
			}
		case "range":
			args = d.Args
		default:
			continue
		}
		for _, ag := range args {
			l, ok := ParseNumericArg(ag)
			if !ok || l.IsWhole() {
				continue
			}
			a.diag(ag.Pos, ag.Pos, lexer.SeverityError, CodeDecoratorTypeMismatch,
				"@%s bound %g must be a whole number on integer %s - codegen compares the bound against an integer value, so a fractional literal would not compile",
				d.Name, l.FloatVal, target)
		}
	}
}

// checkPatternFormatOnBytes rejects `@pattern` and `@format` on a `bytes`
// field; a `bytes @format(raw)` field is left to the [PrimRawBytes] rules.
func (a *analyzer) checkPatternFormatOnBytes(f *ast.Field) {
	if f == nil || f.Type == nil || f.Type.Array || f.Type.Map != nil || f.Type.Named == nil {
		return
	}
	prim := a.primOf(f.Type)
	if prim != "bytes" || HasRawFormat(f.Decorators) {
		return
	}
	for _, d := range f.Decorators {
		if d != nil && (d.Name == "pattern" || d.Name == "format") {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
				"@%s applies to text, not a `bytes` field - a binary value has no string pattern / format, so the runtime validator drops it while the OpenAPI schema would still advertise it. Use a `string` field, or drop the decorator.",
				d.Name)
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
		if d == nil {
			continue
		}
		switch d.Name {
		case "gte", "gt", "lte", "lt", "range", "positive", "negative", "multipleOf",
			"minLength", "maxLength", "length", "pattern", "format":
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
				"@%s cannot constrain a type-parameter field (%s): the parametric validator sees it as `any` and can't enforce the bound, while the monomorphised OpenAPI would still advertise it. Drop the decorator, or constrain a concrete type the instance supplies.",
				d.Name, name)
			return
		}
	}
}
