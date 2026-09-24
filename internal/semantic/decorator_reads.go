package semantic

import (
	"slices"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// IsDeprecated reports whether the decorators mark the construct
// deprecated.
func IsDeprecated(ds []*ast.Decorator) bool {
	return ast.HasDecorator(ds, "deprecated")
}

// DeprecatedReason returns the optional `@deprecated("...")` message, or
// "" when the decorator is absent or carries no argument.
func DeprecatedReason(ds []*ast.Decorator) string {
	reason, _ := ast.StringArg(ds, "deprecated")
	return reason
}

// CrossFieldNames returns the fields an `@requiresOneOf` or
// `@mutuallyExclusive` lists, each once, in order.
func CrossFieldNames(d *ast.Decorator) []string {
	var out []string
	for _, n := range ast.ArgNames(d) {
		if !slices.Contains(out, n.Value) {
			out = append(out, n.Value)
		}
	}
	return out
}

// Description returns a node's `@doc` text, else its leading comment block.
func Description(decs []*ast.Decorator, doc []string) string {
	if s, _ := ast.StringArg(decs, "doc"); s != "" {
		return s
	}
	return strings.Join(doc, "\n")
}

// descriptionLines is [Description] split at the author's line breaks.
func descriptionLines(decs []*ast.Decorator, doc []string) []string {
	desc := Description(decs, doc)
	if desc == "" {
		return nil
	}
	return strings.Split(desc, "\n")
}

// FieldIsRequired reports whether f must be present: it is neither optional
// nor defaulted.
func FieldIsRequired(f *ast.Field) bool {
	if f == nil || f.Type == nil || f.Type.Optional {
		return false
	}
	return !ast.HasDecorator(f.Decorators, "default")
}
