package golang

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// escapeErrorfName makes name safe inside a generated fmt.Errorf format
// literal: Go string escapes, and `%` doubled.
func escapeErrorfName(name string) string {
	q := strconv.Quote(name)
	q = q[1 : len(q)-1]
	return strings.ReplaceAll(q, "%", "%%")
}

// errSubject renders the "<name>: " prefix of a validation message, or "" for
// the subject-less message of a scalar's or enum's own Validate().
func errSubject(name string) string {
	if name == "" {
		return ""
	}
	return name + ": "
}

// shape hands body each element of an array, the dereferenced value of a
// nil-guarded pointer, or access itself.
func shape(f *ast.Field, access string, ctx emitCtx, body func(elem string) string) string {
	switch {
	case f.Type != nil && f.Type.Array:
		return fmt.Sprintf("for i := range %s {\n%s\n}", access, body(access+"[i]"))
	case goFieldIsPointer(f, ctx.pkg, ctx.resolver):
		// Parenthesised so a method call applies to the dereferenced value.
		return fmt.Sprintf("if %s != nil {\n%s\n}", access, body("(*"+access+")"))
	default:
		return body(access)
	}
}

// ifReturnf renders `if cond { return fmt.Errorf(msg) }` and registers the fmt import.
func ifReturnf(cond, msg string, ctx emitCtx) string {
	ctx.uses["fmt"] = true
	return fmt.Sprintf("if %s {\n\treturn fmt.Errorf(%s)\n}", cond, msg)
}

// indentBlock indents every line of s after the first by one tab.
func indentBlock(s string) string {
	return strings.ReplaceAll(s, "\n", "\n\t")
}

// fieldWireName returns the name f travels under - its binding name or its
// JSON key - escaped by [escapeErrorfName] for a message literal.
func fieldWireName(f *ast.Field) string {
	kind := wire.BindingKind(f.Decorators)
	name := wire.JSONName(f)
	switch kind {
	case wire.BindingPath, wire.BindingQuery, wire.BindingHeader, wire.BindingCookie, wire.BindingForm:
		name = wire.WireName(f, kind)
	}
	return escapeErrorfName(name)
}
