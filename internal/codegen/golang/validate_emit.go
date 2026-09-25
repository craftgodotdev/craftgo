package golang

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// escapeErrorf makes s safe inside a generated fmt.Errorf format literal: Go
// string escapes, and `%` doubled.
func escapeErrorf(s string) string {
	q := strconv.Quote(s)
	q = q[1 : len(q)-1]
	return strings.ReplaceAll(q, "%", "%%")
}

// errorf renders the fmt.Errorf call of a validation error, "<subject>: text",
// or text alone for the subject-less error of a scalar's or enum's own
// Validate().
func errorf(subject, text string, ctx emitCtx) string {
	ctx.uses["fmt"] = true
	if subject != "" {
		text = subject + ": " + text
	}
	return `fmt.Errorf("` + escapeErrorf(text) + `")`
}

// failIf renders `if cond { return err }`, err the [errorf] of subject and text.
func failIf(cond, subject, text string, ctx emitCtx) string {
	return fmt.Sprintf("if %s {\n\treturn %s\n}", cond, errorf(subject, text, ctx))
}

// guardBlock renders body inside `if access != nil { ... }`.
func guardBlock(access, body string) string {
	return fmt.Sprintf("if %s != nil {\n%s\n}", access, body)
}

// fieldWireName returns the name f travels under: its binding name or its
// JSON key.
func fieldWireName(f *ast.Field) string {
	kind, _ := wire.BindingKind(f.Decorators)
	if kind.IsParam() {
		return wire.WireName(f, kind)
	}
	return wire.JSONName(f)
}
