package semantic

import (
	"fmt"
	"math"
	"strconv"

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
			if fl, ok := d.Args[0].Value.(*ast.FloatLit); ok && !isIntegralFloat(fl.Value) {
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
	a.diagNegativeUnsigned(f.Decorators, prim)
	// 0 is in range, so the capacity check passes `@lt(0)`.
	for _, d := range f.Decorators {
		if d != nil && d.Name == "lt" && len(d.Args) == 1 && argIsZero(d.Args[0].Value) {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
				"@lt(0) cannot apply to an unsigned type (%s is always >= 0) - every value would be rejected; use a signed integer or a positive bound", prim)
		}
	}
}

// argIsZero reports whether v is the literal 0 or 0.0.
func argIsZero(v ast.Expr) bool {
	switch lit := v.(type) {
	case *ast.IntLit:
		return lit.Value == 0
	case *ast.FloatLit:
		return lit.Value == 0
	}
	return false
}

// diagNegativeUnsigned reports every `@negative` in decs against unsigned
// prim.
func (a *analyzer) diagNegativeUnsigned(decs []*ast.Decorator, prim string) {
	for _, d := range decs {
		if d != nil && d.Name == "negative" {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
				"@negative cannot apply to an unsigned type (%s is always >= 0) - every value would be rejected; use a signed integer or drop @negative", prim)
		}
	}
}

// checkBoundCapacity rejects a numeric bound outside the range of f's
// integer or float32 type, as a constant that would not compile.
func (a *analyzer) checkBoundCapacity(f *ast.Field) {
	if f == nil || f.Type == nil || f.Type.Array || f.Type.Named == nil {
		return
	}
	prim := a.primOf(f.Type)
	if lo, hi, ok := prims.Capacity(prim); ok {
		forEachNumericBound(f, func(d *ast.Decorator, arg *ast.DecoratorArg) {
			if v, disp, ok := integralBoundValue(arg); ok && (v < lo || v > hi) {
				a.diag(arg.Pos, arg.Pos, lexer.SeverityError, CodeBoundOverflow,
					"@%s bound %s exceeds %s range [%g, %g]", d.Name, disp, prim, lo, hi)
			}
		})
		return
	}
	if prim == "float32" {
		forEachNumericBound(f, func(d *ast.Decorator, arg *ast.DecoratorArg) {
			var v float64
			var disp string
			switch lit := arg.Value.(type) {
			case *ast.IntLit:
				v, disp = float64(lit.Value), strconv.FormatInt(lit.Value, 10)
			case *ast.FloatLit:
				v, disp = lit.Value, strconv.FormatFloat(lit.Value, 'g', -1, 64)
			default:
				return
			}
			if v > math.MaxFloat32 || v < -math.MaxFloat32 {
				a.diag(arg.Pos, arg.Pos, lexer.SeverityError, CodeBoundOverflow,
					"@%s bound %s exceeds float32 range [%g, %g]", d.Name, disp, -math.MaxFloat32, math.MaxFloat32)
			}
		})
	}
}

// integralBoundValue returns a whole-number bound, an int or an integral
// float such as 300.0, with its text; ok is false for anything else.
func integralBoundValue(arg *ast.DecoratorArg) (float64, string, bool) {
	switch lit := arg.Value.(type) {
	case *ast.IntLit:
		return float64(lit.Value), strconv.FormatInt(lit.Value, 10), true
	case *ast.FloatLit:
		if !isIntegralFloat(lit.Value) {
			return 0, "", false
		}
		return lit.Value, strconv.FormatFloat(lit.Value, 'f', -1, 64), true
	}
	return 0, "", false
}

// isIntegralFloat reports whether v is a whole number, including one beyond
// the int64 range.
func isIntegralFloat(v float64) bool {
	return v == math.Trunc(v)
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
// or `@range` bound on an integer target; an integral float such as 1e3 passes.
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
			fl, ok := fractionalArg(ag)
			if !ok {
				continue
			}
			a.diag(ag.Pos, ag.Pos, lexer.SeverityError, CodeDecoratorTypeMismatch,
				"@%s bound %g must be a whole number on integer %s - codegen compares the bound against an integer value, so a fractional literal would not compile",
				d.Name, fl.Value, target)
		}
	}
}

// fractionalArg returns a's float literal when it has a fractional part.
func fractionalArg(a *ast.DecoratorArg) (*ast.FloatLit, bool) {
	if a == nil {
		return nil, false
	}
	fl, ok := a.Value.(*ast.FloatLit)
	if !ok || fl.Value == math.Trunc(fl.Value) {
		return nil, false
	}
	return fl, true
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
	isParam := false
	for _, tp := range typeParams {
		if tp == name {
			isParam = true
			break
		}
	}
	if !isParam {
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
