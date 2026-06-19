// Numeric-bound sanity checks: unsigned vs negative bounds, integer
// capacity overflow, fractional literals on integer targets, and
// numeric-decorator targeting rules.
package semantic

import (
	"fmt"
	"math"
	"strconv"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// checkMultipleOfTarget rejects `@multipleOf` where the generated validator
// can't enforce what the OpenAPI advertises. Go's `%` operator is
// integer-only: a float field can't be checked at all, and an integer
// field with a fractional divisor (`@multipleOf(2.5)`) can't either - the
// validator silently drops it while the spec still advertises `multipleOf:
// 2.5`. Both are rejected so the spec and the validator agree.
func (a *analyzer) checkMultipleOfTarget(f *ast.Field) {
	if f == nil || f.Type == nil || f.Type.Named == nil {
		return
	}
	prim := f.Type.Named.Name.String()
	if sd, ok := a.pkg.Scalars[prim]; ok {
		prim = sd.Primitive
	}
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
		// Integer field: a fractional divisor is unenforceable by integer
		// modulus, yet the OpenAPI would advertise it - reject it.
		if len(d.Args) == 1 {
			if fl, ok := d.Args[0].Value.(*ast.FloatLit); ok && fl.Value != float64(int64(fl.Value)) {
				a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
					"@multipleOf on an integer field needs a whole-number divisor - Go's modulus is integer-only, so a fractional divisor can't be enforced (the OpenAPI would advertise a bound the validator drops). Use a whole number.")
			}
		}
	}
}

// unsignedPrim reports whether a Go primitive name is an unsigned
// integer. `@negative` is contradictory on these (the value is always
// >= 0); `@positive` stays legal (it rejects only 0).
func unsignedPrim(prim string) bool {
	switch prim {
	case "uint", "uint8", "uint16", "uint32", "uint64":
		return true
	}
	return false
}

// integerPrim reports whether prim is a signed or unsigned integer
// primitive - the set whose @multipleOf is enforced with Go's modulus.
func integerPrim(prim string) bool {
	switch prim {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64":
		return true
	}
	return false
}

// checkNegativeOnUnsigned rejects `@negative` on an unsigned-integer
// field. The validator emits a `value >= 0` rejection, which fires for
// EVERY value of a `uint*` (always >= 0) - the field could never
// validate. Resolves through a named scalar so `count Quantity
// @negative` (scalar Quantity uint) is caught the same way a bare
// `count uint @negative` is.
func (a *analyzer) checkNegativeOnUnsigned(f *ast.Field) {
	if f == nil || f.Type == nil || f.Type.Named == nil {
		return
	}
	prim := f.Type.Named.Name.String()
	if sd, ok := a.pkg.Scalars[prim]; ok {
		prim = sd.Primitive
	}
	if !unsignedPrim(prim) {
		return
	}
	a.diagNegativeUnsigned(f.Decorators, prim)
	// `@lt(0)` is the desugared spelling of `@negative`: "value < 0", which
	// no `uint*` can satisfy. The capacity check ([checkBoundCapacity])
	// misses it because 0 is itself an in-range value - only the predicate
	// is empty. (`@lt(N)` / `@lte(N)` with N < 0 are already caught there
	// as out-of-range literals.)
	for _, d := range f.Decorators {
		if d != nil && d.Name == "lt" && len(d.Args) == 1 {
			if il, ok := d.Args[0].Value.(*ast.IntLit); ok && il.Value == 0 {
				a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
					"@lt(0) cannot apply to an unsigned type (%s is always >= 0) - every value would be rejected; use a signed integer or a positive bound", prim)
			}
		}
	}
}

// diagNegativeUnsigned emits the `@negative`-on-unsigned diagnostic for
// every `@negative` in decs. Shared by the field and scalar passes.
func (a *analyzer) diagNegativeUnsigned(decs []*ast.Decorator, prim string) {
	for _, d := range decs {
		if d != nil && d.Name == "negative" {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
				"@negative cannot apply to an unsigned type (%s is always >= 0) - every value would be rejected; use a signed integer or drop @negative", prim)
		}
	}
}

// intCapacity returns the value range a Go integer primitive can hold.
// Returns ok=false for non-integer or unrecognised primitives so the
// caller skips the check rather than emit a false-positive overflow
// diagnostic.
func intCapacity(primitive string) (lo, hi float64, ok bool) {
	switch primitive {
	case "int8":
		return -128, 127, true
	case "int16":
		return -32768, 32767, true
	case "int32":
		return -2147483648, 2147483647, true
	case "int64", "int":
		// `int` is 32 or 64-bit depending on platform; treat as the
		// narrower of the two so designs stay portable.
		return -9223372036854775808, 9223372036854775807, true
	case "uint8":
		return 0, 255, true
	case "uint16":
		return 0, 65535, true
	case "uint32":
		return 0, 4294967295, true
	case "uint64", "uint":
		// `uint` follows the same portable-narrow rule as int.
		return 0, 18446744073709551615, true
	}
	return 0, 0, false
}

// checkBoundCapacity rejects numeric bound literals that exceed the
// field's primitive type capacity. Without this, codegen happily emits
// `if v.Small > 300 { ... }` against an `int8` field, which fails to
// compile because 300 overflows int8. float64 captures every literal we
// accept, but a float32 field whose bound magnitude exceeds the float32
// range still emits a float32 literal that overflows and won't compile, so
// it gets its own guard.
func (a *analyzer) checkBoundCapacity(f *ast.Field) {
	if f == nil || f.Type == nil || f.Type.Array || f.Type.Named == nil {
		return
	}
	prim := f.Type.Named.Name.String()
	if sd, ok := a.pkg.Scalars[prim]; ok {
		prim = sd.Primitive
	}
	if lo, hi, ok := intCapacity(prim); ok {
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

// integralBoundValue extracts a whole-number bound value (an IntLit, or an
// INTEGRAL FloatLit like `@gte(300.0)` that renders to a bare whole-number Go
// literal). A fractional float is rejected separately by
// checkIntBoundFloatLiteral, so it returns ok=false here.
func integralBoundValue(arg *ast.DecoratorArg) (float64, string, bool) {
	switch lit := arg.Value.(type) {
	case *ast.IntLit:
		return float64(lit.Value), strconv.FormatInt(lit.Value, 10), true
	case *ast.FloatLit:
		if lit.Value != float64(int64(lit.Value)) {
			return 0, "", false
		}
		return lit.Value, strconv.FormatInt(int64(lit.Value), 10), true
	}
	return 0, "", false
}

// forEachNumericBound invokes check for every numeric-bound decorator argument
// on f: the single-arg comparisons (@gt/@gte/@lt/@lte/@multipleOf) and both
// endpoints of dual-arg @range.
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

// checkBoundLiteralKind rejects a fractional float bound on an
// integer-typed field. Resolves the field's primitive (following a
// local scalar to its underlying type) and defers to the shared
// per-decorator scan.
func (a *analyzer) checkBoundLiteralKind(f *ast.Field) {
	if f == nil || f.Type == nil || f.Type.Array || f.Type.Named == nil {
		return
	}
	prim := f.Type.Named.Name.String()
	if sd, ok := a.pkg.Scalars[prim]; ok {
		prim = sd.Primitive
	}
	a.checkIntBoundFloatLiteral(prim, fmt.Sprintf("field %q", f.Name), f.Decorators)
}

// checkIntBoundFloatLiteral rejects a fractional float bound literal
// (`@gte(0.5)`, `@range(0.5, 10.5)`, …) on an integer-typed target.
// codegen renders a comparison/range bound verbatim, so the literal
// `0.5` ends up compared against the integer field value - Go rejects
// that with "constant 0.5 truncated to integer" and the whole
// generated package fails to build. Catching it here turns an opaque
// downstream build error into a precise design-time diagnostic.
//
// Only genuinely fractional literals are rejected: an integral float
// (`1.0`, `1e3`) renders to a whole-number Go literal and compiles
// fine, and float-typed targets are skipped entirely because a
// fractional bound is exactly what they are for. `@multipleOf` is not
// included - its codegen takes the integer-only path and never emits a
// float literal.
func (a *analyzer) checkIntBoundFloatLiteral(prim, target string, decs []*ast.Decorator) {
	if _, _, ok := intCapacity(prim); !ok {
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

// fractionalArg reports a FloatLit argument whose value carries a
// fractional part. An integral float literal renders to a whole-number
// Go literal, so it is not flagged.
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

// checkPatternFormatOnBytes rejects `@pattern` / `@format` on a `bytes`
// field (or a bytes-backed scalar). Both decorators constrain TEXT - the
// validator emits a regexp / format check gated on a string shape, so a
// bytes field silently drops the check while the OpenAPI schema still
// advertises the pattern / format. A binary value has no string pattern;
// the author wants a `string` field.
func (a *analyzer) checkPatternFormatOnBytes(f *ast.Field) {
	if f == nil || f.Type == nil || f.Type.Array || f.Type.Map != nil || f.Type.Named == nil {
		return
	}
	prim := f.Type.Named.Name.String()
	if sd, ok := a.pkg.Scalars[prim]; ok {
		prim = sd.Primitive
	}
	if prim != "bytes" {
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

// checkValueConstraintOnTypeParam rejects a field-level VALUE constraint
// (numeric or string) on a bare type-parameter field (`val T @gte(10)` in
// a generic decl). The validator is emitted once against the parametric
// receiver, where the element is `any`-constrained and so can't be
// compared / measured - yet the monomorphised OpenAPI advertises the
// bound, diverging from the (silently absent) runtime check. Array-shape
// constraints (@minItems / @maxItems) are NOT rejected here: they bound
// the slice length, which is knowable parametrically (@uniqueItems is
// handled separately by [checkUniqueItemsComparable]).
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
