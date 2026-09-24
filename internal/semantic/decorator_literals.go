package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
)

// ResolveDefaultValue is [ResolveDecoratorLiteral] for `@default`.
func ResolveDefaultValue(f *ast.Field, pkg *Package) (any, bool) {
	return ResolveDecoratorLiteral(f, pkg, "default")
}

// ResolveDecoratorLiteral returns the literal of f's decName decorator, with
// enum member names, alone or in an array, resolved to their wire values.
func ResolveDecoratorLiteral(f *ast.Field, pkg *Package, decName string) (any, bool) {
	if f == nil {
		return nil, false
	}
	for _, d := range f.Decorators {
		if d == nil || d.Name != decName || len(d.Args) == 0 {
			continue
		}
		// An array field's element type names the enum too.
		enumName := ""
		if f.Type != nil && f.Type.Named != nil && f.Type.Named.Name != nil {
			enumName = f.Type.Named.Name.String()
		}
		switch v := d.Args[0].Value.(type) {
		case *ast.IdentExpr:
			if v.Name == nil {
				return nil, false
			}
			if wire, ok := ResolveEnumMember(pkg, enumName, v.Name.String()); ok {
				return wire, true
			}
			return v.Name.String(), true
		case *ast.ArrayLit:
			out := make([]any, 0, len(v.Elements))
			for _, el := range v.Elements {
				if id, ok := el.(*ast.IdentExpr); ok && id.Name != nil {
					if wire, ok := ResolveEnumMember(pkg, enumName, id.Name.String()); ok {
						out = append(out, wire)
					} else {
						out = append(out, id.Name.String())
					}
					continue
				}
				x, ok := LiteralToAny(el)
				if !ok {
					return nil, false
				}
				out = append(out, x)
			}
			return out, true
		default:
			return LiteralToAny(d.Args[0].Value)
		}
	}
	return nil, false
}

// ResolveEnumMember returns the wire value of member in enum enumName, or
// false when pkg has no such enum or member.
func ResolveEnumMember(pkg *Package, enumName, member string) (any, bool) {
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

// ExampleValue is [ResolveDecoratorLiteral] for `@example`.
func ExampleValue(f *ast.Field, pkg *Package) (any, bool) {
	return ResolveDecoratorLiteral(f, pkg, "example")
}

// LiteralToAny converts a literal, arrays included, to its Go value; ok is
// false for any other expression.
func LiteralToAny(e ast.Expr) (any, bool) {
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
			x, ok := LiteralToAny(el)
			if !ok {
				return nil, false
			}
			out = append(out, x)
		}
		return out, true
	}
	return nil, false
}
