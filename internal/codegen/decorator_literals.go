// Resolution of value-bearing decorators (`@default`, `@example`) to the
// wire value the OpenAPI document and the transport pre-fill both use.
package codegen

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// resolveDefaultValue resolves a field's `@default` to a typed value for
// `openapi3.Schema.Default`. When the field is an enum type and the default
// is a bare member identifier (`@default(active)`), it returns the member's
// WIRE value (`= 1` / `= "ACTIVE"`), not the DSL spelling.
func resolveDefaultValue(f *ast.Field, pkg *semantic.Package) (any, bool) {
	return resolveDecoratorLiteral(f, pkg, "default")
}

// resolveDecoratorLiteral resolves the literal value of a value-bearing
// decorator (`@default` / `@example`) on a field, resolving an enum-member
// identifier (or an array of them) to its wire value. Shared so @example
// and @default agree - without it, @example silently drops a bare
// enum-member ident that @default handles.
func resolveDecoratorLiteral(f *ast.Field, pkg *semantic.Package, decName string) (any, bool) {
	if f == nil {
		return nil, false
	}
	for _, d := range f.Decorators {
		if d.Name != decName || len(d.Args) == 0 {
			continue
		}
		// The enum a member identifier resolves against - for an array
		// field (`Method[]`) the element type carries the enum name too, so
		// the same lookup serves both `@default(Card)` and `@default([Card,
		// Bank])`.
		enumName := ""
		if f.Type != nil && f.Type.Named != nil && f.Type.Named.Name != nil {
			enumName = f.Type.Named.Name.String()
		}
		switch v := d.Args[0].Value.(type) {
		case *ast.IdentExpr:
			if v.Name == nil {
				return nil, false
			}
			if wire, ok := resolveEnumMember(pkg, enumName, v.Name.String()); ok {
				return wire, true
			}
			return v.Name.String(), true
		case *ast.ArrayLit:
			// Resolve each element so an array of enum members lands its
			// wire values (`[Card, Bank]` -> `["card", "bank"]`); without
			// this the whole array default is dropped, because literalToAny
			// has no member-ident case and bails on the first one.
			out := make([]any, 0, len(v.Elements))
			for _, el := range v.Elements {
				if id, ok := el.(*ast.IdentExpr); ok && id.Name != nil {
					if wire, ok := resolveEnumMember(pkg, enumName, id.Name.String()); ok {
						out = append(out, wire)
					} else {
						out = append(out, id.Name.String())
					}
					continue
				}
				x, ok := literalToAny(el)
				if !ok {
					return nil, false
				}
				out = append(out, x)
			}
			return out, true
		default:
			return literalToAny(d.Args[0].Value)
		}
	}
	return nil, false
}

// resolveEnumMember returns the wire value of enumName's `member` (the
// `= 1` / `= "ACTIVE"` literal), or ok=false when enumName is not an enum
// in pkg or has no such member.
func resolveEnumMember(pkg *semantic.Package, enumName, member string) (any, bool) {
	if pkg == nil || enumName == "" {
		return nil, false
	}
	ed, ok := pkg.Enums[enumName]
	if !ok {
		return nil, false
	}
	for _, ev := range ed.EnumValues() {
		if ev.Name == member {
			return enumMemberWire(ev), true
		}
	}
	return nil, false
}

// exampleValue extracts an `@example(v)` argument as a typed Go value
// suitable for `openapi3.Schema.Example`. Strings, ints, floats, and
// booleans all round-trip through YAML correctly when assigned to the
// `any` Example field. Array literals (`@example(["a", "b"])`) become
// `[]any` so array-typed properties get a sensible YAML rendering.
// Returns (nil, false) when no example decorator is present so the
// caller leaves the schema untouched.
func exampleValue(f *ast.Field, pkg *semantic.Package) (any, bool) {
	return resolveDecoratorLiteral(f, pkg, "example")
}

// literalToAny converts an [ast.Expr] literal into the equivalent Go
// runtime value. Arrays recurse so nested literals (e.g. an array of
// ints) round-trip without losing element types. Unsupported nodes
// return (nil, false) and the caller skips emission.
func literalToAny(e ast.Expr) (any, bool) {
	switch v := e.(type) {
	case *ast.StringLit:
		return v.Value, true
	case *ast.IntLit:
		return v.Value, true
	case *ast.FloatLit:
		return v.Value, true
	case *ast.BoolLit:
		return v.Value, true
	case *ast.NullLit:
		return nil, true
	case *ast.ArrayLit:
		out := make([]any, 0, len(v.Elements))
		for _, el := range v.Elements {
			x, ok := literalToAny(el)
			if !ok {
				return nil, false
			}
			out = append(out, x)
		}
		return out, true
	}
	return nil, false
}
