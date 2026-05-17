package semantic

// Numeric value-range and combination checks. Sits between the
// argument-shape pass (kind-correct, count-correct) and codegen, so
// every numeric pair we observe here is well-formed AST. We catch:
//
//   - `@length(min, max)`, `@range(min, max)` - min must be ≤ max.
//   - `@minLength` paired with `@maxLength` on the same field - same
//     ordering rule applied across decorators.
//   - `@minItems` / `@maxItems` pair - likewise.
//   - `@min` / `@max` numeric pair - likewise.
//   - `@multipleOf(0)` - divides nothing; codegen would emit a runtime
//     %0 panic.
//   - `@status(code)` - must be in 100..599 (HTTP status range).
//   - Duration / size literals - must be > 0 (timeout 0s is a footgun;
//     0-byte cap rejects every request).
//   - `@nullable` on `T?` field - redundant per README §"Field
//     presence semantics" (warning, not error).

import (
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
	}
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

// checkMultipleOf rejects `@multipleOf(0)` - division by zero would
// panic at runtime; the user almost always meant a positive divisor.
func (a *analyzer) checkMultipleOf(d *ast.Decorator) {
	pos := positionalArgs(d)
	if len(pos) != 1 {
		return
	}
	v, ok := numericValue(pos[0].Value)
	if !ok || v != 0 {
		return
	}
	a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
		"@multipleOf: divisor must not be 0")
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

// checkPairOrdering enforces "min decorator ≤ max decorator" when both
// appear on the same field. Missing one of the pair is fine - the
// solo decorator is unconstrained.
func (a *analyzer) checkPairOrdering(f *ast.Field) {
	pairs := []struct{ lo, hi string }{
		{"minLength", "maxLength"},
		{"minItems", "maxItems"},
		{"min", "max"},
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
