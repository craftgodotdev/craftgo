package semantic

import (
	"math"
	"slices"
	"strconv"
	"strings"

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

// checkPairOrdering rejects a lower bound in decs above an upper bound on
// the same quantity, or meeting it where either is strict: no value, length
// or item count satisfies both.
func (a *analyzer) checkPairOrdering(decs []*ast.Decorator) {
	bs := declaredBounds(decs)
	for _, lo := range bs {
		for _, hi := range bs {
			if !lo.lower || hi.lower || lo.dec == hi.dec || lo.limits != hi.limits {
				continue
			}
			code := CodeDecoratorRange
			switch c := lo.value.Cmp(hi.value); {
			case c < 0, c == 0 && !lo.strict && !hi.strict:
				continue
			case c == 0:
				code = CodeBoundEmptyRange
			}
			diag := a.diag(hi.pos, hi.pos, lexer.SeverityError, code,
				"%s contradicts %s: no %s is both %s and %s",
				decoratorCall(hi.dec), decoratorCall(lo.dec), lo.limits, lo.relation(), hi.relation())
			diag.Related = related(lo.pos, decoratorCall(lo.dec)+" declared here")
		}
	}
}

// boundSide is where one argument of a bound decorator puts the limit: below
// or above the values it admits, and whether it admits the limit itself.
type boundSide struct {
	lower, strict bool
}

// boundDecorators gives each bound decorator what it limits and the side of
// each argument in order; a flag bounds at 0 on its one side.
var boundDecorators = map[string]struct {
	limits string
	sides  []boundSide
}{
	"gt":        {"value", []boundSide{{lower: true, strict: true}}},
	"gte":       {"value", []boundSide{{lower: true}}},
	"lt":        {"value", []boundSide{{strict: true}}},
	"lte":       {"value", []boundSide{{}}},
	"range":     {"value", []boundSide{{lower: true}, {}}},
	"positive":  {"value", []boundSide{{lower: true, strict: true}}},
	"negative":  {"value", []boundSide{{strict: true}}},
	"minLength": {"length", []boundSide{{lower: true}}},
	"maxLength": {"length", []boundSide{{}}},
	"minItems":  {"item count", []boundSide{{lower: true}}},
	"maxItems":  {"item count", []boundSide{{}}},
}

// bound is the limit one argument of a bound decorator puts on what it
// limits: `@gte(5)` a value of at least 5, `@positive` one above 0.
type bound struct {
	boundSide
	dec    *ast.Decorator
	limits string
	value  NumericLit
	// text is the limit as written; pos is the argument's position, the
	// decorator's for a flag.
	text string
	pos  lexer.Position
}

// relation renders the values b admits, such as `≥ 5`.
func (b bound) relation() string {
	switch {
	case b.lower && b.strict:
		return "> " + b.text
	case b.lower:
		return "≥ " + b.text
	case b.strict:
		return "< " + b.text
	}
	return "≤ " + b.text
}

// declaredBounds returns the bounds the first decorator of each bound name
// in decs puts; one whose arguments do not fit its [Spec] or are not
// numbers puts none.
func declaredBounds(decs []*ast.Decorator) []bound {
	var out []bound
	seen := map[string]bool{}
	for _, d := range decs {
		spec, ok := boundDecorators[d.Name]
		if !ok || seen[d.Name] {
			continue
		}
		seen[d.Name] = true
		if rs, _ := DecoratorSpec(d.Name); rs.Args.Max == 0 {
			out = append(out, bound{boundSide: spec.sides[0], dec: d, limits: spec.limits, value: NumericLit{IsInt: true}, text: "0", pos: d.Pos})
			continue
		}
		args := positionalArgs(d)
		if len(args) != len(spec.sides) {
			continue
		}
		values := make([]NumericLit, len(args))
		for i, arg := range args {
			if values[i], ok = ParseNumericArg(arg); !ok {
				break
			}
		}
		if !ok {
			continue
		}
		for i, side := range spec.sides {
			out = append(out, bound{boundSide: side, dec: d, limits: spec.limits, value: values[i], text: literalText(args[i].Value), pos: args[i].Pos})
		}
	}
	return out
}

// decoratorCall renders d with its literal arguments as written: `@gte(5)`,
// `@range(1, 5)`, `@positive`.
func decoratorCall(d *ast.Decorator) string {
	args := positionalArgs(d)
	if len(args) == 0 {
		return "@" + d.Name
	}
	parts := make([]string, len(args))
	for i, arg := range args {
		parts[i] = literalText(arg.Value)
	}
	return "@" + d.Name + "(" + strings.Join(parts, ", ") + ")"
}

// literalText renders literal e as the design writes it.
func literalText(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.StringLit:
		return strconv.Quote(v.Value)
	case *ast.IntLit:
		return strconv.FormatInt(v.Value, 10)
	case *ast.FloatLit:
		return v.Text
	case *ast.BoolLit:
		return strconv.FormatBool(v.Value)
	case *ast.IdentExpr:
		if v.Name != nil {
			return v.Name.String()
		}
	}
	return exprKind(e)
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
		if n, whole := l.wholeInt(); whole && (n.Cmp(lo) < 0 || n.Cmp(hi) > 0) {
			a.diag(pos, pos, lexer.SeverityError, CodeBoundOverflow,
				"%s %s exceeds %s range [%s, %s]", what, n, prim, lo, hi)
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
