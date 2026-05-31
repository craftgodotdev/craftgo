package semantic

// Numeric value-range and combination checks. Sits between the
// argument-shape pass (kind-correct, count-correct) and codegen, so
// every numeric pair we observe here is well-formed AST. We catch:
//
//   - `@length(min, max)`, `@range(min, max)` - min must be ≤ max.
//   - `@minLength` paired with `@maxLength` on the same field - same
//     ordering rule applied across decorators.
//   - `@minItems` / `@maxItems` pair - likewise.
//   - `@gte` / `@lte` numeric bound pair (and the strict `@gt` / `@lt`
//     and mixed combinations) - the lower bound must be ≤ the upper.
//   - `@multipleOf(0)` - divides nothing; codegen would emit a runtime
//     %0 panic.
//   - `@status(code)` - must be in 100..599 (HTTP status range).
//   - Duration / size literals - must be > 0 (timeout 0s is a footgun;
//     0-byte cap rejects every request).
//   - `@nullable` on `T?` field - redundant per README §"Field
//     presence semantics" (warning, not error).

import (
	"fmt"
	"math"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// checkRangesAndExtras runs every per-decorator value sanity rule plus
// the cross-decorator pair checks. Called after the args pass so we
// know each decorator's positional count and kinds are sound.
func (a *analyzer) checkRangesAndExtras(files []*ast.File) {
	for _, f := range files {
		for _, d := range f.Decls {
			a.checkDeclRanges(d)
		}
	}
}

// checkDeclRanges dispatches by declaration kind. Type / error bodies
// are walked field-by-field; service methods get their own dispatch.
func (a *analyzer) checkDeclRanges(d ast.Decl) {
	switch dd := d.(type) {
	case *ast.TypeDecl:
		a.checkBodyRanges(dd.Body)
	case *ast.ErrorDecl:
		a.checkBodyRanges(dd.Body)
	case *ast.ScalarDecl:
		a.checkDecoratorRanges(dd.Decorators)
		// A scalar's bound decorators are inherited into the validator
		// of every field that uses it, so the float-on-integer check
		// must run on the scalar declaration as well as on plain fields.
		a.checkIntBoundFloatLiteral(dd.Primitive, fmt.Sprintf("scalar %q", dd.Name), dd.Decorators)
		if unsignedPrim(dd.Primitive) {
			a.diagNegativeUnsigned(dd.Decorators, dd.Primitive)
		}
	case *ast.ServiceDecl:
		for _, m := range dd.Methods() {
			a.checkDecoratorRanges(m.Decorators)
		}
	}
}

// checkBodyRanges runs the per-field combination rules in addition to
// the per-decorator value checks. Mixin members are skipped.
func (a *analyzer) checkBodyRanges(members []ast.TypeMember) {
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
	}
}

// checkMultipleOfTarget rejects `@multipleOf` on float-typed fields.
// Go's `%` operator is integer-only; the existing codegen drops the
// check silently for float fields, which left the OpenAPI side
// (`multipleOf: 0.5`) inconsistent with the runtime side (no check).
// Approximating modulo for floats requires a tolerance argument the
// DSL doesn't carry, so the safer fix is to reject at semantic time
// and let users layer a custom check inside their service handler.
func (a *analyzer) checkMultipleOfTarget(f *ast.Field) {
	if f == nil || f.Type == nil || f.Type.Named == nil {
		return
	}
	prim := f.Type.Named.Name.String()
	if sd, ok := a.pkg.Scalars[prim]; ok {
		prim = sd.Primitive
	}
	if prim != "float32" && prim != "float64" {
		return
	}
	for _, d := range f.Decorators {
		if d != nil && d.Name == "multipleOf" {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
				"@multipleOf does not support float fields — Go's modulus operator is integer-only. Move the field to an integer type or add a tolerance check in your handler.")
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

// checkNegativeOnUnsigned rejects `@negative` on an unsigned-integer
// field. The validator emits a `value >= 0` rejection, which fires for
// EVERY value of a `uint*` (always >= 0) — the field could never
// validate. Catch the contradiction at semantic time instead of
// generating an always-failing validator. Resolves through a named
// scalar so `count Quantity @negative` (scalar Quantity uint) is caught
// the same way a bare `count uint @negative` is.
func (a *analyzer) checkNegativeOnUnsigned(f *ast.Field) {
	if f == nil || f.Type == nil || f.Type.Named == nil {
		return
	}
	prim := f.Type.Named.Name.String()
	if sd, ok := a.pkg.Scalars[prim]; ok {
		prim = sd.Primitive
	}
	if unsignedPrim(prim) {
		a.diagNegativeUnsigned(f.Decorators, prim)
	}
}

// diagNegativeUnsigned emits the `@negative`-on-unsigned diagnostic for
// every `@negative` in decs. Shared by the field and scalar passes.
func (a *analyzer) diagNegativeUnsigned(decs []*ast.Decorator, prim string) {
	for _, d := range decs {
		if d != nil && d.Name == "negative" {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
				"@negative cannot apply to an unsigned type (%s is always >= 0) — every value would be rejected; use a signed integer or drop @negative", prim)
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
// compile because 300 overflows int8. Floats are skipped — Go float64
// captures every IntLit we accept, and float64 bounds for float32
// fields are tolerated.
func (a *analyzer) checkBoundCapacity(f *ast.Field) {
	if f == nil || f.Type == nil || f.Type.Array || f.Type.Named == nil {
		return
	}
	prim := f.Type.Named.Name.String()
	if sd, ok := a.pkg.Scalars[prim]; ok {
		prim = sd.Primitive
	}
	lo, hi, ok := intCapacity(prim)
	if !ok {
		return
	}
	check := func(d *ast.Decorator, val *ast.IntLit, pos lexer.Position) {
		v := float64(val.Value)
		if v < lo || v > hi {
			a.diag(pos, pos, lexer.SeverityError, CodeBoundOverflow,
				"@%s bound %d exceeds %s range [%g, %g]",
				d.Name, val.Value, prim, lo, hi)
		}
	}
	for _, d := range f.Decorators {
		if d == nil {
			continue
		}
		// All single-arg comparison decorators + dual-arg @range.
		switch d.Name {
		case "gt", "gte", "lt", "lte", "multipleOf":
			if len(d.Args) == 1 {
				if il, ok := d.Args[0].Value.(*ast.IntLit); ok {
					check(d, il, d.Args[0].Pos)
				}
			}
		case "range":
			for _, ag := range d.Args {
				if il, ok := ag.Value.(*ast.IntLit); ok {
					check(d, il, ag.Pos)
				}
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
// `0.5` ends up compared against the integer field value — Go rejects
// that with "constant 0.5 truncated to integer" and the whole
// generated package fails to build. Catching it here turns an opaque
// downstream build error into a precise design-time diagnostic.
//
// Only genuinely fractional literals are rejected: an integral float
// (`1.0`, `1e3`) renders to a whole-number Go literal and compiles
// fine, and float-typed targets are skipped entirely because a
// fractional bound is exactly what they are for. `@multipleOf` is not
// included — its codegen takes the integer-only path and never emits a
// float literal.
func (a *analyzer) checkIntBoundFloatLiteral(prim, target string, decs []*ast.Decorator) {
	if _, _, ok := intCapacity(prim); !ok {
		return // not an integer primitive — float bounds are valid
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
				"@%s bound %g must be a whole number on integer %s — codegen compares the bound against an integer value, so a fractional literal would not compile",
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

// checkDecoratorRanges applies value-sanity checks to each decorator
// in the slice. The dispatch table is small and explicit so adding a
// new rule means adding one case here plus the helper.
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

// checkPairArgs handles `@length(min, max)` / `@range(min, max)`. The
// 1-arg form of `@length` is "exact length" and skipped here.
func (a *analyzer) checkPairArgs(d *ast.Decorator) {
	pos := positionalArgs(d)
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

// checkMultipleOf rejects non-positive divisors. Zero panics at
// runtime (division by zero). Negative divisors are mathematically
// valid in Go (`%` follows the dividend sign) but every common
// interpretation of "multiple of N" means N > 0 — accepting negatives
// silently lets a typo (`@multipleOf(-2)`) compile to a validator
// that exhibits surprising symmetry around the dividend's sign.
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

// checkHTTPStatus rejects `@status(code)` outside the 100..599 range.
// Tightening to a known-status set is intentionally avoided - RFC
// allows future additions and we don't want to lag the spec.
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

// checkPositiveDuration rejects `@timeout(0)` etc. Bare-int form
// (interpreted as seconds) is also checked. Negative values are
// likewise rejected.
func (a *analyzer) checkPositiveDuration(d *ast.Decorator) {
	pos := positionalArgs(d)
	if len(pos) != 1 {
		return
	}
	if v, ok := pos[0].Value.(*ast.IntLit); ok && v.Value <= 0 {
		a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
			"@%s: duration must be > 0 (got %d)", d.Name, v.Value)
	}
}

// checkPositiveSize rejects `@maxBodySize(0)` - accepts any request
// silently. Negative sizes are nonsensical.
func (a *analyzer) checkPositiveSize(d *ast.Decorator) {
	pos := positionalArgs(d)
	if len(pos) != 1 {
		return
	}
	if v, ok := pos[0].Value.(*ast.IntLit); ok && v.Value <= 0 {
		a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
			"@%s: size must be > 0 (got %d)", d.Name, v.Value)
	}
}

// checkNonNegativeInt rejects negative @minLength etc. Length / item
// counts cannot be negative; if the user wrote -1 they likely meant 0.
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

// checkPairOrdering enforces "lower decorator ≤ upper decorator" when
// both appear on the same field. Missing one of the pair is fine — the
// solo decorator is unconstrained. Four pair families:
//
//   - String length: `@minLength` vs `@maxLength`
//   - Array items:   `@minItems` vs `@maxItems`
//   - Numeric (inclusive): `@gte` vs `@lte`
//   - Numeric (strict):    `@gt`  vs `@lt`
//
// Mixed strict/inclusive pairs (`@gte(5) @lt(5)` etc.) are inspected
// for emptiness — when at least one bound is strict and the endpoints
// touch, no value satisfies both checks. Without this, codegen happily
// emits a validator that rejects every input.
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
		// Equal endpoints define an empty value set whenever EITHER
		// bound is strict — `@gt(5) @lte(5)` excludes 5 on the
		// lower side, `@gte(5) @lt(5)` excludes 5 on the upper side,
		// `@gt(5) @lt(5)` excludes 5 on both. Only fully-inclusive
		// `@gte(N) @lte(N)` accepts the single value N.
		if loV == hiV && (p.loStrict || p.hiStrict) {
			diag := a.diag(hiPos, hiPos, lexer.SeverityWarning, CodeBoundEmptyRange,
				"@%s(%g) combined with @%s(%g) defines an empty range — no value satisfies both",
				p.hi, hiV, p.lo, loV)
			diag.Related = related(loPos, "@"+p.lo+" declared here")
		}
	}
}

// checkNullableRedundant warns when `@nullable` is applied to a `T?`
// field. README §"Field presence semantics" splits the four states
// explicitly; the optional marker already conveys nullability so the
// decorator is noise.
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

// singleNumericArg looks up the first decorator named `name` in decs
// and extracts its first numeric positional argument. Returns the
// numeric value, the position the IDE should underline, and ok=false
// when the decorator is absent or its first arg isn't numeric.
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

// numericValue extracts a float64 from an int or float literal. Other
// expr kinds return ok=false.
func numericValue(e ast.Expr) (float64, bool) {
	switch v := e.(type) {
	case *ast.IntLit:
		return float64(v.Value), true
	case *ast.FloatLit:
		return v.Value, true
	}
	return 0, false
}
