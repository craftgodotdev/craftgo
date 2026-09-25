package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
)

// ResolveDefaultValue returns the literal of f's `@default`, with enum
// member names, alone or in an array, resolved to their wire values.
func ResolveDefaultValue(f *ast.Field, pkg *Package) (any, bool) {
	return resolveDecoratorLiteral(f, pkg, "default")
}

// ExampleValue is [ResolveDefaultValue] for `@example`.
func ExampleValue(f *ast.Field, pkg *Package) (any, bool) {
	return resolveDecoratorLiteral(f, pkg, "example")
}

// resolveDecoratorLiteral returns the literal of f's decName decorator, with
// enum member names, alone or in an array, resolved to their wire values.
func resolveDecoratorLiteral(f *ast.Field, pkg *Package, decName string) (any, bool) {
	if f == nil {
		return nil, false
	}
	for _, d := range f.Decorators {
		if d.Name != decName || len(d.Args) == 0 {
			continue
		}
		// An array field's element type names the enum too.
		enumName := ""
		if f.Type != nil && f.Type.Named != nil && f.Type.Named.Name != nil {
			enumName = f.Type.Named.Name.String()
		}
		return literalValue(d.Args[0].Value, func(name string) any {
			if wire, ok := resolveEnumMember(pkg, enumName, name); ok {
				return wire
			}
			return name
		})
	}
	return nil, false
}

// literalValue converts literal e, arrays included, to its Go value, with
// ident giving an identifier's; ok is false for any other expression.
func literalValue(e ast.Expr, ident func(name string) any) (any, bool) {
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
	case *ast.IdentExpr:
		if v.Name == nil {
			return nil, false
		}
		return ident(v.Name.String()), true
	case *ast.ArrayLit:
		out := make([]any, 0, len(v.Elements))
		for _, el := range v.Elements {
			x, ok := literalValue(el, ident)
			if !ok {
				return nil, false
			}
			out = append(out, x)
		}
		return out, true
	}
	return nil, false
}

// resolveEnumMember returns the wire value of member in enum enumName, or
// false when pkg has no such enum or member.
func resolveEnumMember(pkg *Package, enumName, member string) (any, bool) {
	if pkg == nil || enumName == "" {
		return nil, false
	}
	ed, ok := pkg.Enums[enumName]
	if !ok {
		return nil, false
	}
	for _, ev := range ed.EnumValues() {
		if ev.Name == member {
			return EnumMemberWire(ev), true
		}
	}
	return nil, false
}
