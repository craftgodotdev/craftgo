package semantic

import (
	"regexp"
	"slices"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// checkDecoratorArgs checks every known decorator at s: its argument shape
// against its [Spec], then, when the shape holds, the values it takes.
func (a *analyzer) checkDecoratorArgs(s decoratorSite) {
	for _, d := range s.decs {
		spec, ok := DecoratorSpec(d.Name)
		if !ok {
			continue
		}
		if a.checkDecoratorShape(d, spec) {
			a.checkDecoratorValue(d)
		}
	}
}

// checkDecoratorShape checks d's arguments against spec - empty `()` on a
// flag, an object where a literal belongs, then the positional arguments -
// and reports whether they hold.
func (a *analyzer) checkDecoratorShape(d *ast.Decorator, spec Spec) bool {
	if spec.Args.Max == 0 && d.HasParens {
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityWarning, CodeFlagEmptyParens,
			"@%s never accepts arguments - drop the parens (canonical: `@%s`). `craftgo fmt` fixes this on save.",
			d.Name, d.Name)
	}
	exampleOK := a.checkExampleArg(d)
	return a.checkPositionalArgs(d, spec) && exampleOK
}

// checkDecoratorValue checks the argument values of d, a decorator whose
// arguments fit its [Spec].
func (a *analyzer) checkDecoratorValue(d *ast.Decorator) {
	switch d.Name {
	case "pattern":
		a.checkPatternArg(d)
	case "group":
		a.checkGroupArg(d)
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

// checkPatternArg rejects a `@pattern` that is empty or not a valid RE2
// expression.
func (a *analyzer) checkPatternArg(d *ast.Decorator) {
	s, ok := d.Args[0].Value.(*ast.StringLit)
	if !ok {
		return
	}
	if s.Value == "" {
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorArgType,
			"@pattern requires a non-empty regular expression - an empty pattern matches everything and is not a meaningful constraint")
		return
	}
	if _, err := regexp.Compile(s.Value); err != nil {
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorArgType,
			"@pattern is not a valid regular expression: %v - the generated validator compiles it with regexp.MustCompile, which would panic at startup", err)
	}
}

// checkGroupArg requires a `@group` path to have at least one segment and
// only segments of letters, digits, '-' and '_'; extra slashes are ignored.
func (a *analyzer) checkGroupArg(d *ast.Decorator) {
	s, ok := d.Args[0].Value.(*ast.StringLit)
	if !ok {
		return
	}
	hasSegment := false
	for _, seg := range strings.Split(s.Value, "/") {
		if seg == "" {
			continue // tolerated: leading / trailing / doubled slash
		}
		hasSegment = true
		if seg == "." || seg == ".." {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorArgValue,
				"@group segment %q is not allowed - @group names the directory the block's generated files go to, in place of the service's own, and must be a plain relative path like \"admin\" or \"admin/ops\"", seg)
			return
		}
		if !isPlainPathSegment(seg) {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorArgValue,
				"@group segment %q must contain only letters, digits, '-' or '_'", seg)
			return
		}
	}
	if !hasSegment {
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorArgValue,
			"@group value %q is empty after trimming slashes - provide a path like \"admin\" or \"admin/ops\"", s.Value)
	}
}

// isPlainPathSegment reports whether s is a non-empty run of ASCII letters,
// digits, '-' and '_'.
func isPlainPathSegment(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return s != ""
}

// checkPairArgs requires min ≤ max in `@length` and `@range`, and
// non-negative `@length` values.
func (a *analyzer) checkPairArgs(d *ast.Decorator) {
	pos := positionalArgs(d)
	if d.Name == "length" && len(pos) == 1 {
		if l, ok := ParseNumericArg(pos[0]); ok && l.FloatVal < 0 {
			a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
				"@length: exact length must be ≥ 0 (got %g)", l.FloatVal)
		}
		return
	}
	loLit, loOk := ParseNumericArg(pos[0])
	hiLit, hiOk := ParseNumericArg(pos[1])
	if !loOk || !hiOk {
		return
	}
	lo, hi := loLit.FloatVal, hiLit.FloatVal
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
	l, ok := ParseNumericArg(pos[0])
	if !ok {
		return
	}
	v := l.FloatVal
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
// seconds or as a duration, or that [DurationArg] cannot read.
func (a *analyzer) checkPositiveDuration(d *ast.Decorator) {
	pos := positionalArgs(d)
	dur, ok := DurationArg(pos[0])
	switch v := pos[0].Value.(type) {
	case *ast.DurationLit:
		switch {
		case !ok:
			a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
				"@%s: %s is not a duration (suffix must be one of %s)",
				d.Name, v.Text, strings.Join(lexer.DurationUnits, ", "))
		case dur <= 0:
			a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
				"@%s: duration must be > 0 (got %s)", d.Name, v.Text)
		}
	case *ast.IntLit:
		switch {
		case v.Value <= 0:
			a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
				"@%s: duration must be > 0 (got %d)", d.Name, v.Value)
		case !ok:
			a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
				"@%s: %d seconds is out of range for a duration (at most %d)", d.Name, v.Value, maxDurationSeconds)
		}
	}
}

// checkPositiveSize rejects a `@maxBodySize` or `@maxSize` of zero or less,
// which would mean no cap, or one [lexer.ParseSize] cannot read.
func (a *analyzer) checkPositiveSize(d *ast.Decorator) {
	pos := positionalArgs(d)
	if v, ok := pos[0].Value.(*ast.SizeLit); ok {
		if _, parsed := lexer.ParseSize(v.Text); !parsed {
			a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
				"@%s: %s is not a byte size (suffix must be one of %s, and the count must fit in an int64)",
				d.Name, v.Text, strings.Join(lexer.SizeSuffixes(), ", "))
			return
		}
	}
	if n, ok := sizeBytes(pos[0].Value); ok && n <= 0 {
		a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
			"@%s: size must be > 0 (got %d)", d.Name, n)
	}
}

// checkNonNegativeInt rejects a negative length or item count.
func (a *analyzer) checkNonNegativeInt(d *ast.Decorator) {
	pos := positionalArgs(d)
	if v, ok := pos[0].Value.(*ast.IntLit); ok && v.Value < 0 {
		a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
			"@%s: value must be ≥ 0 (got %d)", d.Name, v.Value)
	}
}

// checkExampleArg rejects an `@example` argument that is an object, which the
// parser leaves with a nil Value, and reports whether there is none.
func (a *analyzer) checkExampleArg(d *ast.Decorator) bool {
	if d.Name != "example" {
		return true
	}
	for _, ag := range positionalArgs(d) {
		if ag.Value == nil {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorArgType,
				"@example takes a literal (string/int/float/bool) or an array of those, not an object - a struct example is composed from each field's own @example; for a free-form any/map field, describe the shape with @doc")
			return false
		}
	}
	return true
}

// positionalArgs returns d's unnamed arguments, objects included.
func positionalArgs(d *ast.Decorator) []*ast.DecoratorArg {
	var out []*ast.DecoratorArg
	for _, ag := range d.Args {
		if ag == nil || ag.Named {
			continue
		}
		out = append(out, ag)
	}
	return out
}

// checkPositionalArgs checks d's arguments against [Spec.Args]: no named
// arguments, then the count, then each kind and the first argument's enum;
// it reports whether they hold.
func (a *analyzer) checkPositionalArgs(d *ast.Decorator, spec Spec) bool {
	ok := true
	for _, ag := range d.Args {
		if ag.Named {
			a.diag(ag.Pos, ag.Pos, lexer.SeverityError, CodeDecoratorArgType,
				"@%s: named argument %q is not supported (use positional args)", d.Name, ag.Name)
			ok = false
		}
	}
	pos := positionalArgs(d)
	rule := spec.Args

	// `@name([a, b])` stands for `@name(a, b)`.
	if rule.AllowArrayShortcut && len(pos) == 1 {
		if arr, isArray := pos[0].Value.(*ast.ArrayLit); isArray {
			shortcutOK := a.checkArrayShortcut(d, rule, arr)
			enumOK := a.checkEnumOnFirst(d, spec, pos)
			return ok && shortcutOK && enumOK
		}
	}

	if len(pos) < rule.Min {
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorArity,
			"@%s expects at least %d argument(s), got %d", d.Name, rule.Min, len(pos))
		return false
	}
	if rule.Max >= 0 && len(pos) > rule.Max {
		extra := pos[rule.Max]
		a.diag(extra.Pos, extra.Pos, lexer.SeverityError, CodeDecoratorArity,
			"@%s accepts at most %d argument(s), got %d", d.Name, rule.Max, len(pos))
		return false
	}

	for i, ag := range pos {
		want := rule.Variadic
		if i < len(rule.Kinds) {
			want = rule.Kinds[i]
		}
		if !exprMatchesKind(ag.Value, want) {
			a.diag(ag.Pos, ag.Pos, lexer.SeverityError, CodeDecoratorArgType,
				"@%s arg %d: expected %s, got %s", d.Name, i+1, want, exprKind(ag.Value))
			ok = false
		}
	}

	return a.checkEnumOnFirst(d, spec, pos) && ok
}

// checkArrayShortcut checks the array literal standing for d's arguments
// against rule's Min, Max and Variadic, and reports whether it holds.
func (a *analyzer) checkArrayShortcut(d *ast.Decorator, rule ArgsRule, arr *ast.ArrayLit) bool {
	n := len(arr.Elements)
	if n < rule.Min {
		a.diag(arr.Pos, arr.Pos, lexer.SeverityError, CodeDecoratorArity,
			"@%s expects at least %d element(s) in array, got %d", d.Name, rule.Min, n)
		return false
	}
	if rule.Max >= 0 && n > rule.Max {
		a.diag(arr.Elements[rule.Max].ExprPos(), arr.Elements[rule.Max].ExprPos(),
			lexer.SeverityError, CodeDecoratorArity,
			"@%s accepts at most %d element(s) in array, got %d", d.Name, rule.Max, n)
		return false
	}
	ok := true
	for i, el := range arr.Elements {
		if !exprMatchesKind(el, rule.Variadic) {
			a.diag(el.ExprPos(), el.ExprPos(), lexer.SeverityError, CodeDecoratorArgType,
				"@%s array[%d]: expected %s, got %s", d.Name, i, rule.Variadic, exprKind(el))
			ok = false
		}
	}
	return ok
}

// checkEnumOnFirst rejects a first argument outside spec.Args.Enum, and
// warns when a valid value is spelled as a string, not an identifier; it
// reports whether the argument is in the set.
func (a *analyzer) checkEnumOnFirst(d *ast.Decorator, spec Spec, pos []*ast.DecoratorArg) bool {
	enum := spec.Args.Enum
	if len(enum) == 0 || len(pos) == 0 {
		return true
	}
	val, ok := ast.TextValue(pos[0].Value)
	if !ok {
		return true
	}
	if !slices.Contains(enum, val) {
		a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorArgValue,
			"@%s arg 1: %q is not a valid value (expected one of: %s)",
			d.Name, val, joinQuoted(enum))
		return false
	}
	if _, isStr := pos[0].Value.(*ast.StringLit); isStr {
		a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityWarning, CodeArgPreferIdent,
			"@%s arg 1: prefer bare identifier `%s` over string \"%s\" (`craftgo fmt` rewrites this on save)",
			d.Name, val, val)
	}
	return true
}

// exprMatchesKind reports whether e fits kind k; ArgAny matches even nil.
func exprMatchesKind(e ast.Expr, k ArgKind) bool {
	return k == ArgAny || slices.Contains(argKinds[k].accepts, exprKind(e))
}

// exprKind names e's kind for the "expected X, got Y" message: its literal
// form, "(no value)" for nil, or "value" for an expression it does not list.
func exprKind(e ast.Expr) string {
	switch e.(type) {
	case nil:
		return "(no value)"
	case *ast.StringLit:
		return "string"
	case *ast.IntLit:
		return "int"
	case *ast.FloatLit:
		return "float"
	case *ast.BoolLit:
		return "bool"
	case *ast.NullLit:
		return "null"
	case *ast.IdentExpr:
		return "identifier"
	case *ast.DurationLit:
		return "duration"
	case *ast.SizeLit:
		return "size"
	case *ast.ArrayLit:
		return "array"
	}
	return "value"
}

// joinQuoted renders xs as `"a", "b", "c"`, in input order.
func joinQuoted(xs []string) string {
	if len(xs) == 0 {
		return ""
	}
	out := make([]byte, 0, len(xs)*8)
	for i, x := range xs {
		if i > 0 {
			out = append(out, ',', ' ')
		}
		out = append(out, '"')
		out = append(out, x...)
		out = append(out, '"')
	}
	return string(out)
}
