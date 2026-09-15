package semantic

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// Target-neutral reads of decorators that carry a plain fact rather than
// a rendering rule. Every target answers these the same way, so they are
// resolved once here instead of per emitter.
//
// These read decorators that have passed analysis: [CodeDecoratorDuplicate]
// rejects a repeated non-repeatable decorator and [CodeDecoratorArity]
// rejects extra arguments, which is what makes reading the first match and
// the first argument sufficient.

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

// DecoratorStringArg returns the string argument of the named decorator
// and whether it was found, distinguishing an absent decorator from one
// carrying an empty string. Covers the plain "decorator-with-text" forms
// - `@doc("...")`, `@summary("...")`, `@deprecated("...")`,
// `@contract("...")`; richer shapes have their own extractors.
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

// Description returns the documentation for a node, preferring an
// explicit `@doc("...")` over the leading comment block: the decorator is
// an intentional override, while comments are often implementation notes.
func Description(decs []*ast.Decorator, doc []string) string {
	if s, _ := DecoratorStringArg(decs, "doc"); s != "" {
		return s
	}
	return strings.Join(doc, "\n")
}

// descriptionLines is [Description] as the comment lines a target
// renders, one `//` apiece: the `@doc("...")` override when present,
// otherwise the leading comment block. Nothing is re-wrapped - the
// generated comment breaks where the author's text breaks.
func descriptionLines(decs []*ast.Decorator, doc []string) []string {
	desc := Description(decs, doc)
	if desc == "" {
		return nil
	}
	return strings.Split(desc, "\n")
}

// FieldIsRequired is the spec-required rule under craftgo's
// "required by default" model: a field must be present unless its type
// carries the `?` suffix, or it carries `@default` - the transport
// pre-fills a default, so the caller may omit the field.
func FieldIsRequired(f *ast.Field) bool {
	if f == nil || f.Type == nil || f.Type.Optional {
		return false
	}
	return !ast.HasDecorator(f.Decorators, "default")
}
