package semantic

import (
	"cmp"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/strfmt"
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
// each argument in order; a flag bounds at 0 on its one side, and a
// one-argument `@length` bounds the length on both sides.
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
	"length":    {"length", []boundSide{{lower: true}, {}}},
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
		if d.Name == "length" && len(args) == 1 {
			args = []*ast.DecoratorArg{args[0], args[0]}
		}
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

// admits reports whether q, a value of primitive prim or a length or item
// count, lies within b; a float compares at its primitive's width, as the
// generated check does, anything else exactly.
func (b bound) admits(prim string, q NumericLit) bool {
	c := q.Cmp(b.value)
	if sp, ok := prims.Lookup(prim); ok && sp.Kind == prims.Float {
		c = cmp.Compare(q.FloatVal, b.value.FloatVal)
		if sp.Bits == 32 {
			c = cmp.Compare(float32(q.FloatVal), float32(b.value.FloatVal))
		}
	}
	switch {
	case b.lower && b.strict:
		return c > 0
	case b.lower:
		return c >= 0
	case b.strict:
		return c < 0
	}
	return c <= 0
}

// constraintSite is a decorator list that constrains a value, and how a
// diagnostic names it: "" for the field's own, ` of scalar <Name>` for its
// type's.
type constraintSite struct {
	decs []*ast.Decorator
	of   string
}

// valueConstraintSites returns the sites that constrain a value of type t:
// decs, then those of the scalar t names.
func (a *analyzer) valueConstraintSites(t *ast.TypeRef, decs []*ast.Decorator) []constraintSite {
	sites := []constraintSite{{decs: decs}}
	if sd := a.lookupScalar(t.Named); sd != nil {
		sites = append(sites, constraintSite{decs: sd.Decorators, of: " of scalar " + t.Named.Name.String()})
	}
	return sites
}

// checkDefaultConstraints rejects a `@default` of f, whose argument is arg,
// that breaks a constraint f carries or its scalar declares: an array
// default meets f's item constraints and each element its scalar's. An enum
// member is checked as its wire value. A literal of the wrong kind is left
// to [analyzer.checkLiteralType].
func (a *analyzer) checkDefaultConstraints(f *ast.Field, arg *ast.DecoratorArg) {
	t := f.Type
	if t == nil {
		return
	}
	if !t.Array {
		v := a.checkedValue(t, arg.Value)
		a.checkValueConstraints(a.valueConstraintSites(t, f.Decorators), a.checkedPrim(t), v, arg.Pos,
			"@default("+literalText(arg.Value)+")"+wireNote(arg.Value, v))
		return
	}
	arr, ok := arg.Value.(*ast.ArrayLit)
	if !ok {
		return
	}
	elem := t.ElemTypeRef()
	prim := a.checkedPrim(elem)
	subject := "@default(" + literalText(arr) + ")"
	held := fmt.Sprintf("%d items", len(arr.Elements))
	if len(arr.Elements) == 1 {
		held = "1 item"
	}
	for _, b := range declaredBounds(f.Decorators) {
		if b.limits == "item count" && !b.admits("", countLit(len(arr.Elements))) {
			a.diag(arg.Pos, arg.Pos, lexer.SeverityError, CodeDecoratorConflict,
				"%s violates %s: it holds %s", subject, decoratorCall(b.dec), held)
		}
	}
	values := make([]ast.Expr, len(arr.Elements))
	for i, e := range arr.Elements {
		values[i] = a.checkedValue(elem, e)
	}
	if ast.HasDecorator(f.Decorators, "uniqueItems") {
		seen := map[string]bool{}
		for i, v := range values {
			key := literalKey(prim, v)
			if seen[key] {
				a.diag(arg.Pos, arg.Pos, lexer.SeverityError, CodeDecoratorConflict,
					"%s violates @uniqueItems: %s repeats", subject, literalText(arr.Elements[i]))
				break
			}
			seen[key] = true
		}
	}
	sites := a.valueConstraintSites(elem, nil)
	for i, e := range arr.Elements {
		a.checkValueConstraints(sites, prim, values[i], e.ExprPos(), "@default element "+literalText(e)+wireNote(e, values[i]))
	}
}

// wireNote names the wire value checked, where e, as the design writes it,
// is an enum member: `, whose wire value is "a",`.
func wireNote(e, checked ast.Expr) string {
	if checked == e {
		return ""
	}
	return ", whose wire value is " + literalText(checked) + ","
}

// checkedPrim returns the primitive a value of type t is checked as: an
// enum's wire primitive, else [analyzer.primOf].
func (a *analyzer) checkedPrim(t *ast.TypeRef) string {
	if ed := a.lookupEnum(t.Named); ed != nil {
		return EnumPrimitive(ed)
	}
	return a.primOf(t)
}

// checkedValue returns literal e, a value of type t, as its constraints read
// it: a member of the enum t names as the member's wire value.
func (a *analyzer) checkedValue(t *ast.TypeRef, e ast.Expr) ast.Expr {
	ed := a.lookupEnum(t.Named)
	ident, ok := e.(*ast.IdentExpr)
	if ed == nil || !ok || ident.Name == nil {
		return e
	}
	ev := enumMember(ed, ident.Name.String())
	if ev == nil {
		return e
	}
	switch wire := EnumMemberWire(ev).(type) {
	case int64:
		return &ast.IntLit{Pos: ident.Pos, Value: wire}
	case string:
		return &ast.StringLit{Pos: ident.Pos, Value: wire}
	}
	return e
}

// checkValueConstraints reports each constraint of sites that literal v, a
// value of primitive prim written at pos, breaks; subject names v. A
// constraint on another kind of value than v's is left to the type checks.
func (a *analyzer) checkValueConstraints(sites []constraintSite, prim string, v ast.Expr, pos lexer.Position, subject string) {
	if !exprMatchesKind(v, primitiveArgKind(prim)) {
		return
	}
	report := func(d *ast.Decorator, of, format string, args ...any) {
		a.diag(pos, pos, lexer.SeverityError, CodeDecoratorConflict,
			"%s violates %s%s: %s", subject, decoratorCall(d), of, fmt.Sprintf(format, args...))
	}
	for _, site := range sites {
		switch v := v.(type) {
		case *ast.StringLit:
			length := utf8.RuneCountInString(v.Value)
			for _, b := range declaredBounds(site.decs) {
				if b.limits == "length" && !b.admits("", countLit(length)) {
					report(b.dec, site.of, "its length is %d", length)
				}
			}
			for _, d := range site.decs {
				if why := textConstraintBreach(d, v.Value); why != "" {
					report(d, site.of, "%s", why)
				}
			}
		case *ast.IntLit, *ast.FloatLit:
			q, _ := ParseNumeric(v)
			for _, b := range declaredBounds(site.decs) {
				if b.limits == "value" && !b.admits(prim, q) {
					report(b.dec, site.of, "it is not %s", b.relation())
				}
			}
			if d := ast.FindDecorator(site.decs, "multipleOf"); d != nil && prims.IsInteger(prim) {
				if div, ok := ParseNumericArg(firstPositional(d)); ok && div.FloatVal != 0 && !new(big.Rat).Quo(q.Rat(), div.Rat()).IsInt() {
					report(d, site.of, "it is not a multiple of %s", literalText(firstPositional(d).Value))
				}
			}
		}
	}
}

// textConstraintBreach says how s breaks d, a `@pattern` or a `@format` the
// generated check validates, or returns "".
func textConstraintBreach(d *ast.Decorator, s string) string {
	arg := firstPositional(d)
	if arg == nil {
		return ""
	}
	switch d.Name {
	case "pattern":
		pattern, _ := ast.TextValue(arg.Value)
		if re, err := regexp.Compile(pattern); err == nil && !re.MatchString(s) {
			return "it does not match"
		}
	case "format":
		name, _ := ast.TextValue(arg.Value)
		if spec, ok := strfmt.Lookup(name); ok && !spec.Valid(s) {
			return "it is not a valid " + spec.Label
		}
	}
	return ""
}

// countLit returns n, a length or an item count, as a [NumericLit].
func countLit(n int) NumericLit {
	return NumericLit{IntVal: int64(n), FloatVal: float64(n), IsInt: true}
}

// firstPositional returns d's first unnamed argument, or nil.
func firstPositional(d *ast.Decorator) *ast.DecoratorArg {
	if args := positionalArgs(d); len(args) > 0 {
		return args[0]
	}
	return nil
}

// literalKey is the value literal e holds as an element of primitive prim,
// so that two literals of one value, such as 1.0 and 1.00, share it.
func literalKey(prim string, e ast.Expr) string {
	if l, ok := ParseNumeric(e); ok {
		if sp, isPrim := prims.Lookup(prim); isPrim && sp.Kind == prims.Float {
			if sp.Bits == 32 {
				return "n" + strconv.FormatFloat(float64(float32(l.FloatVal)), 'g', -1, 32)
			}
			return "n" + strconv.FormatFloat(l.FloatVal, 'g', -1, 64)
		}
		return "n" + l.Rat().RatString()
	}
	if s, ok := e.(*ast.StringLit); ok {
		return "s" + s.Value
	}
	return fmt.Sprintf("%T:%s", e, literalText(e))
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

// literalText renders literal e as the design writes it; a string written
// over several lines is quoted onto one.
func literalText(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.StringLit:
		if v.Text != "" && !strings.ContainsAny(v.Text, "\r\n") {
			return v.Text
		}
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
	case *ast.NullLit:
		return "null"
	case *ast.ArrayLit:
		parts := make([]string, len(v.Elements))
		for i, el := range v.Elements {
			parts[i] = literalText(el)
		}
		return "[" + strings.Join(parts, ", ") + "]"
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
