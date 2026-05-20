package semantic

// Decorator argument validation. Walks every decorator already passed
// the placement check and verifies its positional arguments match the
// [ArgsRule] declared on its [Spec].
//
// Three diagnostic codes fire here:
//   - [CodeDecoratorArity]    - wrong number of positional arguments.
//   - [CodeDecoratorArgType]  - literal kind doesn't match the slot.
//   - [CodeDecoratorArgValue] - value outside an allowed enum set.

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// checkDecoratorArgs walks every decorator in every scope and validates
// argument shape against its registry [Spec]. Unknown decorators were
// already flagged by the placement pass; this pass skips them so the
// IDE doesn't double-report.

func (a *analyzer) checkDecoratorArgs(files []*ast.File) {
	for _, f := range files {
		a.checkArgsScope(LvlFile, f.Decorators)
		for _, d := range f.Decls {
			a.checkDeclArgs(d)
		}
	}
}

// checkDeclArgs dispatches argument validation for one top-level decl
// plus every nested scope it owns.
func (a *analyzer) checkDeclArgs(d ast.Decl) {
	switch dd := d.(type) {
	case *ast.TypeDecl:
		a.checkArgsScope(LvlType, dd.Decorators)
		a.checkFieldArgs(LvlField, dd.Body)
	case *ast.EnumDecl:
		a.checkArgsScope(LvlEnum, dd.Decorators)
		for _, v := range dd.EnumValues() {
			a.checkArgsScope(LvlEnumValue, v.Decorators)
		}
	case *ast.ErrorDecl:
		a.checkArgsScope(LvlError, dd.Decorators)
		a.checkFieldArgs(LvlErrorField, dd.Body)
	case *ast.ScalarDecl:
		a.checkArgsScope(LvlScalar, dd.Decorators)
	case *ast.MiddlewareDecl:
		a.checkArgsScope(LvlMiddleware, dd.Decorators)
	case *ast.ServiceDecl:
		if !dd.Extend {
			a.checkArgsScope(LvlService, dd.Decorators)
		}
		for _, m := range dd.Methods() {
			a.checkArgsScope(LvlMethod, m.Decorators)
		}
	}
}

// checkFieldArgs walks fields in a type or error body. Mixin members
// have no decorators and are skipped. site is [LvlField] for type
// bodies and [LvlErrorField] for error bodies.
func (a *analyzer) checkFieldArgs(site Level, members []ast.TypeMember) {
	for _, m := range members {
		f, ok := m.(*ast.Field)
		if !ok {
			continue
		}
		a.checkArgsScope(site, f.Decorators)
		a.checkFieldDefault(f)
	}
}

func (a *analyzer) checkArgsScope(site Level, decs []*ast.Decorator) {
	for _, d := range decs {
		if d == nil {
			continue
		}
		spec, ok := Lookup(d.Name)
		if !ok {
			continue
		}
		a.checkDecoratorArg(site, d, spec)
	}
}

// checkDecoratorArg validates one decorator against its [Spec]. Flag
// decorators (`@positive`, `@uniqueItems`, ...) warn on empty parens;
// everything else goes through the generic positional check.
func (a *analyzer) checkDecoratorArg(site Level, d *ast.Decorator, spec Spec) {
	// Flag decorators never take arguments — empty parens are
	// stylistically wrong. Warn (not error); `craftgo fmt` strips
	// them on save.
	if spec.Flag && d.HasParens {
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityWarning, CodeFlagEmptyParens,
			"@%s never accepts arguments — drop the parens (canonical: `@%s`). `craftgo fmt` fixes this on save.",
			d.Name, d.Name)
	}
	a.checkPositionalArgs(site, d, spec)
}

// positionalArgs splits d.Args into positional vs named. Object and
// nested-decorator args are treated as positional (they have no name)
// so the generic checker can count them against [ArgsRule.Min/Max].
// Per-position kind checks then decide what to do: [ArgAny] passes
// them through; tighter kinds (`ArgString`, `ArgInt`, ...) reject them
// because [exprMatchesKind] returns false for `nil` Value with any
// non-ArgAny kind.
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

// checkPositionalArgs verifies count + per-position kind + first-arg
// enum set against [Spec.Args]. The array-shortcut form
// (`@mimeTypes(["a/b","c/d"])`) is expanded transparently when
// [ArgsRule.AllowArrayShortcut] is set and the decorator received
// exactly one array-literal positional arg - element kinds and count
// are validated against the variadic rule.
//
// Stops on the first arity mismatch because subsequent kind errors
// would just compound the user's confusion.
func (a *analyzer) checkPositionalArgs(site Level, d *ast.Decorator, spec Spec) {
	// Named args are reserved for decorators with custom shape hooks
	// (currently none). Reject them globally so users get a single
	// clear error instead of a silently-ignored argument.
	for _, ag := range d.Args {
		if ag != nil && ag.Named {
			a.diag(ag.Pos, ag.Pos, lexer.SeverityError, CodeDecoratorArgType,
				"@%s: named argument %q is not supported (use positional args)", d.Name, ag.Name)
		}
	}
	pos := positionalArgs(d)
	rule := spec.Args

	// Array shortcut: `@name([v1, v2, ...])` is treated as
	// `@name(v1, v2, ...)`. The expanded form must satisfy the rest of
	// the rule on its own.
	if rule.AllowArrayShortcut && len(pos) == 1 {
		if arr, ok := pos[0].Value.(*ast.ArrayLit); ok {
			a.checkArrayShortcut(d, rule, arr)
			a.checkEnumOnFirst(site, d, spec, pos)
			return
		}
	}

	// Arity. We pin the diagnostic to the decorator name when args are
	// missing (so the IDE underlines `@name`) and to the first extra
	// arg when there are too many (so the squiggle points at the
	// offender).
	if len(pos) < rule.Min {
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorArity,
			"@%s expects at least %d argument(s), got %d", d.Name, rule.Min, len(pos))
		return
	}
	if rule.Max >= 0 && len(pos) > rule.Max {
		extra := pos[rule.Max]
		a.diag(extra.Pos, extra.Pos, lexer.SeverityError, CodeDecoratorArity,
			"@%s accepts at most %d argument(s), got %d", d.Name, rule.Max, len(pos))
		return
	}

	// Per-position kind.
	for i, ag := range pos {
		want := rule.Variadic
		if i < len(rule.Kinds) {
			want = rule.Kinds[i]
		}
		if want == ArgAny {
			continue
		}
		if !exprMatchesKind(ag.Value, want) {
			a.diag(ag.Pos, ag.Pos, lexer.SeverityError, CodeDecoratorArgType,
				"@%s arg %d: expected %s, got %s", d.Name, i+1, want, exprKindName(ag.Value))
		}
	}

	a.checkEnumOnFirst(site, d, spec, pos)
}

// checkArrayShortcut validates the elements of an array passed as the
// sole positional arg. Element count must satisfy [ArgsRule.Min/Max];
// each element must match [ArgsRule.Variadic].
func (a *analyzer) checkArrayShortcut(d *ast.Decorator, rule ArgsRule, arr *ast.ArrayLit) {
	n := len(arr.Elements)
	if n < rule.Min {
		a.diag(arr.Pos, arr.Pos, lexer.SeverityError, CodeDecoratorArity,
			"@%s expects at least %d element(s) in array, got %d", d.Name, rule.Min, n)
		return
	}
	if rule.Max >= 0 && n > rule.Max {
		a.diag(arr.Elements[rule.Max].ExprPos(), arr.Elements[rule.Max].ExprPos(),
			lexer.SeverityError, CodeDecoratorArity,
			"@%s accepts at most %d element(s) in array, got %d", d.Name, rule.Max, n)
		return
	}
	want := rule.Variadic
	if want == ArgAny {
		return
	}
	for i, el := range arr.Elements {
		if !exprMatchesKind(el, want) {
			a.diag(el.ExprPos(), el.ExprPos(), lexer.SeverityError, CodeDecoratorArgType,
				"@%s array[%d]: expected %s, got %s", d.Name, i, want, exprKindName(el))
		}
	}
}

// checkEnumOnFirst applies the value-set check on the first positional
// arg. Bare-int / non-textual args silently skip - the kind check above
// already flagged them.
//
// When the arg is enum-valid but spelled as a STRING (`@format("email")`
// instead of `@format(email)`) the canonical form is the bare ident.
// Emit a soft `CodeArgPreferIdent` warning so the IDE surfaces the
// non-canonical form; `craftgo fmt` rewrites on save.
func (a *analyzer) checkEnumOnFirst(site Level, d *ast.Decorator, spec Spec, pos []*ast.DecoratorArg) {
	_ = site
	enum := spec.Args.Enum
	if len(enum) == 0 || len(pos) == 0 {
		return
	}
	val, ok := identOrStringValue(pos[0].Value)
	if !ok {
		return
	}
	if !inSet(val, enum) {
		a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorArgValue,
			"@%s arg 1: %q is not a valid value (expected one of: %s)",
			d.Name, val, joinQuoted(enum))
		return
	}
	if _, isStr := pos[0].Value.(*ast.StringLit); isStr {
		a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityWarning, CodeArgPreferIdent,
			"@%s arg 1: prefer bare identifier `%s` over string \"%s\" (`craftgo fmt` rewrites this on save)",
			d.Name, val, val)
	}
}

// exprMatchesKind reports whether e is a literal of the given kind.
// Acceptance rules follow the README's "bare number → seconds / bytes"
// convention so:
//
//   - ArgDuration accepts [ast.DurationLit] OR [ast.IntLit] (bare int =
//     seconds);
//   - ArgSize accepts [ast.SizeLit] OR [ast.IntLit] (bare int = bytes);
//   - ArgNumber accepts int and float;
//   - ArgStringOrIdent accepts string or bare ident.
//
// ArgAny matches everything (including nil) - used as a no-op when the
// position is shape-validated by a per-decorator hook instead.
func exprMatchesKind(e ast.Expr, k ArgKind) bool {
	if k == ArgAny {
		return true
	}
	if e == nil {
		return false
	}
	switch k {
	case ArgString:
		_, ok := e.(*ast.StringLit)
		return ok
	case ArgInt:
		_, ok := e.(*ast.IntLit)
		return ok
	case ArgNumber:
		switch e.(type) {
		case *ast.IntLit, *ast.FloatLit:
			return true
		}
		return false
	case ArgBool:
		_, ok := e.(*ast.BoolLit)
		return ok
	case ArgIdent:
		_, ok := e.(*ast.IdentExpr)
		return ok
	case ArgDuration:
		switch e.(type) {
		case *ast.DurationLit, *ast.IntLit:
			return true
		}
		return false
	case ArgSize:
		switch e.(type) {
		case *ast.SizeLit, *ast.IntLit:
			return true
		}
		return false
	case ArgStringOrIdent:
		switch e.(type) {
		case *ast.StringLit, *ast.IdentExpr:
			return true
		}
		return false
	}
	return false
}

// exprKindName renders a human label for the actual kind of e. Used in
// the "expected X, got Y" message so the IDE points the user at the
// concrete mismatch. Falls back to "value" for any future ast.Expr
// implementation we forget to add here - the diagnostic stays useful
// rather than empty.
func exprKindName(e ast.Expr) string {
	if e == nil {
		return "(no value)"
	}
	name := "value"
	switch e.(type) {
	case *ast.StringLit:
		name = "string"
	case *ast.IntLit:
		name = "int"
	case *ast.FloatLit:
		name = "float"
	case *ast.BoolLit:
		name = "bool"
	case *ast.NullLit:
		name = "null"
	case *ast.IdentExpr:
		name = "identifier"
	case *ast.DurationLit:
		name = "duration"
	case *ast.SizeLit:
		name = "size"
	case *ast.ArrayLit:
		name = "array"
	}
	return name
}

// identOrStringValue extracts the textual payload of an [ast.IdentExpr]
// or [ast.StringLit]. Returns ok=false for any other shape so the enum
// check can skip without false positives.
func identOrStringValue(e ast.Expr) (string, bool) {
	switch v := e.(type) {
	case *ast.IdentExpr:
		if v.Name == nil {
			return "", false
		}
		return v.Name.String(), true
	case *ast.StringLit:
		return v.Value, true
	}
	return "", false
}

// inSet reports whether s appears in xs. Linear scan - fine for the
// short fixed sets we use (≤17 entries for the format value list).
func inSet(s string, xs []string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// joinQuoted renders xs as `"a", "b", "c"` for the "expected one of"
// hint. Output order matches input order so a stable test golden value
// is achievable.
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
