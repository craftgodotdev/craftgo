package semantic

import (
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
	reason, _ := DecoratorStringArg(ds, "deprecated")
	return reason
}

// DecoratorStringArg returns the named decorator's string argument and
// whether ds has one; on an analysed design the first match is the only one.
func DecoratorStringArg(ds []*ast.Decorator, name string) (string, bool) {
	for _, d := range ds {
		if d == nil || d.Name != name {
			continue
		}
		for _, arg := range d.Args {
			for _, v := range ast.DecoratorArgValues(arg) {
				if s, ok := v.(*ast.StringLit); ok {
					return s.Value, true
				}
			}
		}
	}
	return "", false
}

// Description returns a node's `@doc` text, else its leading comment block.
func Description(decs []*ast.Decorator, doc []string) string {
	if s, _ := DecoratorStringArg(decs, "doc"); s != "" {
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
