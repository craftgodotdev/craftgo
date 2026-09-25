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

// ifReturnf renders `if cond { return fmt.Errorf(msg) }` and registers the fmt import.
func ifReturnf(cond, msg string, ctx emitCtx) string {
	ctx.uses["fmt"] = true
	return fmt.Sprintf("if %s {\n\treturn fmt.Errorf(%s)\n}", cond, msg)
}

// fieldWireName returns the name f travels under - its binding name or its
// JSON key - escaped by [escapeErrorfName] for a message literal.
func fieldWireName(f *ast.Field) string {
	kind, _ := wire.BindingKind(f.Decorators)
	name := wire.JSONName(f)
	if kind.IsParam() {
		name = wire.WireName(f, kind)
	}
	return escapeErrorfName(name)
}
