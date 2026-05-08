package semantic

// Decorator argument validation. Walks every decorator already passed
// the placement check and verifies its positional arguments match the
// [ArgsRule] declared on its [Spec]. Decorators with non-uniform shapes
// (`@security`, `@example`, `@examples`, `@externalDocs`) are routed to
// per-decorator hooks so the registry stays simple for the 90% case.
//
// Three diagnostic codes fire here:
//   - [CodeDecoratorArity]    - wrong number of positional arguments.
//   - [CodeDecoratorArgType]  - literal kind doesn't match the slot.
//   - [CodeDecoratorArgValue] - value outside an allowed enum set.

import (
	"strings"

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

// checkFieldDefault validates `@default(...)` on a field. Three layers:
//   1. Conflict with `@required` - required fields fail validation
//      before the default branch is reached, so the combo is nonsense.
//   2. Type support - only primitives, enums, scalars wrapping
//      primitives, and arrays of those are emit-able. Map / struct
//      / generic / array-of-struct fields raise decorator/conflict.
//   3. Value form - the literal must match the resolved type:
//      enum field → bare ident matching a declared value;
//      array field → ArrayLit whose elements satisfy the per-element
//      rule recursively.
//
// Primitive / scalar fields pass through unchecked - the kind rule in
// [checkArgsScope] already pins the literal kind.
func (a *analyzer) checkFieldDefault(f *ast.Field) {
	if f == nil {
		return
	}
	var dec *ast.Decorator
	for _, d := range f.Decorators {
		if d != nil && d.Name == "default" {
			dec = d
			break
		}
	}
	if dec == nil {
		return
	}
	if !defaultTypeSupported(f.Type, a.pkg) {
		a.diag(dec.Pos, decoratorEnd(dec), lexer.SeverityError,
			CodeDecoratorConflict,
			"@default is not supported on field %q: only primitives, enums, scalars (wrapping primitives), and arrays of those are allowed",
			f.Name)
		return
	}
	pos := positionalArgs(dec)
	if len(pos) != 1 {
		return
	}
	a.checkDefaultLiteral(f, f.Type, pos[0].Value, pos[0].Pos)
}

// checkDefaultLiteral validates the literal arg against a resolved
// type. Recurses through arrays so `@default([Active, Pending])` on a
// `Status[]` field flags any non-IdentExpr element or unknown enum
// value. For primitive / scalar fields the literal kind must match
// the resolved primitive (string vs int vs bool, ...).
func (a *analyzer) checkDefaultLiteral(f *ast.Field, t *ast.TypeRef, v ast.Expr, pos lexer.Position) {
	if t == nil {
		return
	}
	if t.Array {
		arr, ok := v.(*ast.ArrayLit)
		if !ok {
			a.diag(pos, pos, lexer.SeverityError, CodeDecoratorArgType,
				"@default on array field %q must be an array literal", f.Name)
			return
		}
		elem := arrayElemTypeRef(t)
		for _, e := range arr.Elements {
			a.checkDefaultLiteral(f, elem, e, e.ExprPos())
		}
		return
	}
	if _, ok := v.(*ast.ArrayLit); ok {
		a.diag(pos, pos, lexer.SeverityError, CodeDecoratorArgType,
			"@default on field %q expects a single value, not an array literal", f.Name)
		return
	}
	if t.Named == nil || t.Named.Name == nil || len(t.Named.Name.Parts) != 1 {
		return
	}
	name := t.Named.Name.Parts[0]
	if ed, isEnum := a.pkg.Enums[name]; isEnum {
		ident, ok := v.(*ast.IdentExpr)
		if !ok {
			a.diag(pos, pos, lexer.SeverityError, CodeDecoratorArgValue,
				"@default on enum field %q must reference an enum value by name (one of %s)",
				f.Name, enumValueList(ed))
			return
		}
		if ident.Name == nil || len(ident.Name.Parts) != 1 {
			a.diag(pos, pos, lexer.SeverityError, CodeDecoratorArgValue,
				"@default on enum field %q must be one of %s", f.Name, enumValueList(ed))
			return
		}
		want := ident.Name.Parts[0]
		for _, ev := range ed.EnumValues() {
			if ev.Name == want {
				return
			}
		}
		a.diag(pos, pos, lexer.SeverityError, CodeDecoratorArgValue,
			"@default %q is not a value of enum %s; expected one of %s",
			want, ed.Name, enumValueList(ed))
		return
	}
	want := defaultPrimitiveKind(name, a.pkg)
	if want == ArgAny {
		return
	}
	if !exprMatchesKind(v, want) {
		a.diag(pos, pos, lexer.SeverityError, CodeDecoratorArgType,
			"@default on field %q (%s) requires a %s literal", f.Name, name, want)
	}
}

// defaultPrimitiveKind maps a resolved primitive (or scalar) name to
// the [ArgKind] its `@default` literal must match. Scalars resolve
// through to their underlying primitive in one hop. Returns ArgAny
// for names this layer can't classify so the caller skips the kind
// check rather than emit a misleading mismatch.
func defaultPrimitiveKind(name string, pkg *Package) ArgKind {
	if sd, ok := pkg.Scalars[name]; ok {
		name = sd.Primitive
	}
	switch name {
	case "string", "bytes":
		return ArgString
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64":
		return ArgInt
	case "float32", "float64":
		return ArgNumber
	case "bool":
		return ArgBool
	}
	return ArgAny
}

// defaultTypeSupported reports whether @default may target a field of
// type t. Path C: primitives, enums, scalars wrapping primitives,
// optional of those, and arrays of those are allowed. Map / struct /
// generic / array-of-struct return false so the caller can flag the
// combination. Cross-package qualified refs (multi-segment names)
// also return false - the v1 model doesn't resolve them and the
// codegen path has nothing to emit.
func defaultTypeSupported(t *ast.TypeRef, pkg *semanticPkgRef) bool {
	if t == nil || t.Map != nil {
		return false
	}
	if t.Array {
		return defaultElemSupported(arrayElemTypeRef(t), pkg)
	}
	return defaultElemSupported(t, pkg)
}

// defaultElemSupported is the per-element check used both for
// stand-alone fields and array elements.
func defaultElemSupported(t *ast.TypeRef, pkg *semanticPkgRef) bool {
	if t == nil || t.Named == nil || t.Named.Name == nil {
		return false
	}
	if len(t.Named.Name.Parts) != 1 {
		return false
	}
	name := t.Named.Name.Parts[0]
	if primFromName(name) != 0 {
		return true
	}
	if _, ok := pkg.Enums[name]; ok {
		return true
	}
	if sd, ok := pkg.Scalars[name]; ok {
		return primFromName(sd.Primitive) != 0
	}
	return false
}

// semanticPkgRef is the subset of [Package] that
// [defaultTypeSupported] needs. Lifted to a small interface so the
// helper stays testable without the full analyzer wiring.
type semanticPkgRef = Package

// arrayElemTypeRef returns the element TypeRef of an array. The
// stored TypeRef has Array == true alongside the element's Named /
// Optional fields, so dropping the Array flag yields the element
// type. Optional propagates so `T?[]` element is `T?`.
func arrayElemTypeRef(t *ast.TypeRef) *ast.TypeRef {
	if t == nil {
		return nil
	}
	clone := *t
	clone.Array = false
	return &clone
}

// enumValueList renders an enum's value names as a comma-separated
// list for diagnostic messages.
func enumValueList(ed *ast.EnumDecl) string {
	if ed == nil {
		return ""
	}
	enumVals := ed.EnumValues()
	out := make([]string, 0, len(enumVals))
	for _, v := range enumVals {
		out = append(out, v.Name)
	}
	return strings.Join(out, ", ")
}

// checkArgsScope is the leaf: validate every decorator's args against
// its registered [Spec]. Unknown names are silently skipped - they were
// flagged by the placement pass and we don't want duplicate diagnostics
// for the same source location.
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

// checkDecoratorArg validates one decorator. Routes special-shape
// decorators to their hook; everything else goes through the generic
// positional check.
func (a *analyzer) checkDecoratorArg(site Level, d *ast.Decorator, spec Spec) {
	switch d.Name {
	case "security":
		a.checkSecurityArgs(d)
		return
	case "example":
		a.checkExampleArgs(d)
		return
	case "examples":
		a.checkExamplesArgs(d)
		return
	case "externalDocs":
		a.checkExternalDocsArgs(d)
		return
	}
	a.checkPositionalArgs(site, d, spec)
}

// positionalArgs splits d.Args into positional vs other (named, nested
// decorators, object literals). The generic checker only consults the
// positional slice; per-decorator hooks read named/object args
// directly off d.Args.
func positionalArgs(d *ast.Decorator) []*ast.DecoratorArg {
	var out []*ast.DecoratorArg
	for _, ag := range d.Args {
		if ag == nil || ag.Named || ag.Object != nil || ag.Nested != nil {
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

// ---- Per-decorator hooks ----

// checkSecurityArgs handles `@security(scheme[, scopes: ["a", "b"]])`.
// Acceptance:
//   - exactly 1 positional ident (the scheme name);
//   - at most 1 named arg with name `scopes` whose value is an array of
//     strings.
//
// Cross-references (does the scheme exist in the OpenAPI config?) are
// validated by [analyzer.checkDecoratorRefs].
func (a *analyzer) checkSecurityArgs(d *ast.Decorator) {
	pos := positionalArgs(d)
	if len(pos) != 1 {
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorArity,
			"@security expects exactly 1 scheme name (got %d)", len(pos))
		return
	}
	if _, ok := pos[0].Value.(*ast.IdentExpr); !ok {
		a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorArgType,
			"@security arg 1: expected scheme identifier, got %s", exprKindName(pos[0].Value))
	}
	for _, ag := range d.Args {
		if !ag.Named {
			continue
		}
		if ag.Name != "scopes" {
			a.diag(ag.Pos, ag.Pos, lexer.SeverityError, CodeDecoratorArgType,
				"@security: unexpected named argument %q (only `scopes` is supported)", ag.Name)
			continue
		}
		arr, ok := ag.Value.(*ast.ArrayLit)
		if !ok {
			a.diag(ag.Pos, ag.Pos, lexer.SeverityError, CodeDecoratorArgType,
				"@security scopes: expected array of strings, got %s", exprKindName(ag.Value))
			continue
		}
		for i, el := range arr.Elements {
			if _, ok := el.(*ast.StringLit); !ok {
				a.diag(el.ExprPos(), el.ExprPos(), lexer.SeverityError, CodeDecoratorArgType,
					"@security scopes[%d]: expected string, got %s", i, exprKindName(el))
			}
		}
	}
}

// checkExampleArgs handles `@example(value)` where value may be a
// literal OR an object. Exactly one positional or one object arg.
func (a *analyzer) checkExampleArgs(d *ast.Decorator) {
	count := len(d.Args)
	if count != 1 {
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorArity,
			"@example expects exactly 1 argument (got %d)", count)
		return
	}
	ag := d.Args[0]
	if ag.Named {
		a.diag(ag.Pos, ag.Pos, lexer.SeverityError, CodeDecoratorArgType,
			"@example does not accept named arguments")
	}
}

// checkExamplesArgs handles `@examples({name1: v1, name2: v2})` -
// exactly one object-literal arg.
func (a *analyzer) checkExamplesArgs(d *ast.Decorator) {
	if len(d.Args) != 1 {
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorArity,
			"@examples expects exactly 1 object argument (got %d)", len(d.Args))
		return
	}
	if d.Args[0].Object == nil {
		a.diag(d.Args[0].Pos, d.Args[0].Pos, lexer.SeverityError, CodeDecoratorArgType,
			"@examples expects a {name: value, ...} object literal")
	}
}

// checkExternalDocsArgs handles three accepted forms:
//
//   - `@externalDocs("url")`                                   (positional)
//   - `@externalDocs(url: "...", description: "...")`          (named)
//   - `@externalDocs({url: "...", description: "..."})`        (object)
//
// Allowed keys for the named/object forms are `url` and `description`,
// both of which must be string literals. The positional form accepts a
// single URL string.
func (a *analyzer) checkExternalDocsArgs(d *ast.Decorator) {
	if len(d.Args) == 0 {
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorArity,
			"@externalDocs expects at least 1 argument")
		return
	}
	// Object-literal form.
	if len(d.Args) == 1 && d.Args[0].Object != nil {
		a.checkExternalDocsKVs(d.Args[0].Object)
		return
	}
	// All-named form: every arg must be `url:` or `description:`.
	allNamed := true
	for _, ag := range d.Args {
		if !ag.Named {
			allNamed = false
			break
		}
	}
	if allNamed {
		fields := make([]*ast.ObjectField, 0, len(d.Args))
		for _, ag := range d.Args {
			fields = append(fields, &ast.ObjectField{Pos: ag.Pos, Name: ag.Name, Value: ag.Value})
		}
		a.checkExternalDocsKVs(fields)
		return
	}
	// Positional form: exactly 1 string.
	if len(d.Args) != 1 {
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorArity,
			"@externalDocs positional form expects exactly 1 URL string (got %d args)", len(d.Args))
		return
	}
	if _, ok := d.Args[0].Value.(*ast.StringLit); !ok {
		a.diag(d.Args[0].Pos, d.Args[0].Pos, lexer.SeverityError, CodeDecoratorArgType,
			"@externalDocs: expected URL string, got %s", exprKindName(d.Args[0].Value))
	}
}

// checkExternalDocsKVs validates a {url, description} key/value list,
// shared by the object-literal and all-named forms.
func (a *analyzer) checkExternalDocsKVs(fields []*ast.ObjectField) {
	for _, of := range fields {
		if of.Name != "url" && of.Name != "description" {
			a.diag(of.Pos, of.Pos, lexer.SeverityError, CodeDecoratorArgType,
				"@externalDocs: unknown key %q (allowed: url, description)", of.Name)
			continue
		}
		if _, ok := of.Value.(*ast.StringLit); !ok {
			a.diag(of.Value.ExprPos(), of.Value.ExprPos(), lexer.SeverityError, CodeDecoratorArgType,
				"@externalDocs.%s: expected string, got %s", of.Name, exprKindName(of.Value))
		}
	}
}
