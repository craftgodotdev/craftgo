package codegen

import (
	"fmt"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// fieldWireName returns the name a client uses for f: the wire alias of a bound
// field (the `@path`/`@query`/`@header`/`@cookie`/`@form` name argument, e.g.
// `@header("x-source-domain")`), or f.Name for a body field (whose JSON key is
// the field name). Validation messages use it so a failure reports what the
// caller actually sent - `x-source-domain: ...`, not the DSL field name. The
// scalar synth field (no name, no decorators) maps to "", keeping the shared
// scalar/enum Validate() message subject-less.
func fieldWireName(f *ast.Field) string {
	kind := wire.BindingKind(f.Decorators)
	switch kind {
	case wire.BindingPath, wire.BindingQuery, wire.BindingHeader, wire.BindingCookie, wire.BindingForm:
		return wire.WireName(f, kind)
	default:
		return f.Name
	}
}

// errSubject renders the leading "<field>: " of a validation message, or "" when
// the field has no name. A scalar's / enum's own Validate() body is emitted with
// an empty name so its message carries only the constraint ("length less than
// 3"); the field that uses that type restores the subject by wrapping the error
// with the field name (see nestedValidateCall). Regular struct fields pass their
// real name and keep the "<field>: " prefix.
func errSubject(name string) string {
	if name == "" {
		return ""
	}
	return name + ": "
}

// This file collects every function that produces Go source for a
// validator. Each per-decorator emitter is paired with a comment
// explaining (a) what type-shapes it accepts and (b) what generated
// code it produces. Three cross-cutting helpers - [shape],
// [ifReturnf], and the enum/typeParam/nested call emitters - are
// shared across multiple validators and live at the top of the file.

// shape returns Go source for a field-level check, picking the right
// per-form scaffold (loop / nil-guard / bare). The body builder is
// invoked once with an "element expression" that the body can use as
// the concrete value to inspect:
//
//   - array  → `access[i]` inside `for i := range access {}`
//   - opt    → `*access`   inside `if access != nil {}`
//   - single → `access`    with no wrapping
//
// The body is responsible for any `return ...` it needs; the wrapper
// merely delivers control to it for each element.

func shape(f *ast.Field, access string, body func(elem string) string) string {
	switch {
	case f.Type != nil && f.Type.Array:
		return fmt.Sprintf("for i := range %s {\n%s\n}", access, body(access+"[i]"))
	case goFieldIsPointer(f, nil, nil):
		// Reached only for generic type-param probes, never a direct nilable
		// scalar, so the pointer test needs no scalar resolver.
		// The Go field is *T - from `?` (optional) OR `@nullable`
		// (required-but-nullable). Key on the actual pointer-ness, not
		// just the `?` suffix: a `@nullable` enum/scalar field lowers to
		// *T too. Nil-guard before the deref (a nil *T would panic), then
		// deref. Parenthesise the
		// deref so callers can prefix operators (`len(...)`, `&`, method
		// calls) without Go precedence surprises - `(*v.Avatar).Validate()`
		// works; `*v.Avatar.Validate()` parses as `*(v.Avatar.Validate())`.
		return fmt.Sprintf("if %s != nil {\n%s\n}", access, body("(*"+access+")"))
	default:
		return body(access)
	}
}

// ifReturnf assembles a single multi-line `if cond { return fmt.Errorf(msg) }`
// block. Centralised here so every per-decorator emitter has identical
// output formatting (go/format normalises whitespace afterwards).
func ifReturnf(cond, msg string) string {
	return fmt.Sprintf("if %s {\n\treturn fmt.Errorf(%s)\n}", cond, msg)
}

// indentBlock prefixes every newline in s with a tab so the rendered
// snippet aligns one indent level deeper inside an enclosing if/for
// block. Useful when a per-decorator check produces a multi-line body
// that has to nest under another statement.
func indentBlock(s string) string {
	return strings.ReplaceAll(s, "\n", "\n\t")
}
