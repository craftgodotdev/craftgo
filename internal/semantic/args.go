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
		spec, ok := Lookup(d.Name)
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
				"@group segment %q is not allowed - @group nests generated files under the service directory and must be a plain relative path like \"admin\" or \"admin/ops\"", seg)
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

// checkExampleArg rejects an `@example` argument that is an object or a
// nested decorator, which the parser leaves with a nil Value, and reports
// whether there is none.
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

// positionalArgs returns d's unnamed arguments, objects and nested
// decorators included.
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
