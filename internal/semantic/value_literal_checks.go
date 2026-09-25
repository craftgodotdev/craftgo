package semantic

import (
	"fmt"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// noDefault gives, by primitive kind, why `@default` cannot target a value
// of it.
var noDefault = map[prims.Kind]string{
	prims.Bytes:    "a bytes value has no unambiguous literal form (Go []byte vs OpenAPI base64 `format: byte`)",
	prims.File:     "a file upload has no literal default form",
	prims.DateTime: `a fixed timestamp is rarely the default meant, and "now" is the handler's to decide`,
}

// DefaultNeedsOptional reports whether f carries `@default` without `?`:
// the default fills an absent value, so the field is optional, unless @path
// binds it, as a path segment is always present.
func DefaultNeedsOptional(f *ast.Field) bool {
	return f.Type != nil && !f.Type.Optional && ast.HasDecorator(f.Decorators, "default") &&
		!ast.HasDecorator(f.Decorators, wire.BindingPath)
}

// checkFieldDefault checks f's `@default`: the type it targets, the `?` it
// implies, its literal and the constraints its value meets.
func (a *analyzer) checkFieldDefault(f *ast.Field) {
	dec := ast.FindDecorator(f.Decorators, "default")
	if dec == nil {
		return
	}
	if problem := a.literalTargetProblem("default", f); problem != "" {
		a.diag(dec.Pos, decoratorEnd(dec), lexer.SeverityError, CodeDecoratorConflict, "%s", problem)
		return
	}
	if DefaultNeedsOptional(f) {
		a.diag(dec.Pos, decoratorEnd(dec), lexer.SeverityWarning, CodeDefaultNeedsOptional,
			"@default on non-optional field %q: the default fires when the value is absent, so the field is optional - add `?` (or run `craftgo fmt`) so types.go, validate.go, and the OpenAPI agree it is optional",
			f.Name)
	}
	if args := positionalArgs(dec); len(args) == 1 {
		a.checkLiteralType("default", f, f.Type, args[0].Value, args[0].Pos)
		a.checkDefaultConstraints(f, args[0])
	}
}

// checkFieldExample checks f's `@example` literal against f's type as a
// `@default` is checked.
func (a *analyzer) checkFieldExample(f *ast.Field) {
	dec := ast.FindDecorator(f.Decorators, "example")
	if dec == nil {
		return
	}
	if problem := a.literalTargetProblem("example", f); problem != "" {
		a.diag(dec.Pos, decoratorEnd(dec), lexer.SeverityError, CodeDecoratorConflict, "%s", problem)
		return
	}
	for _, ag := range positionalArgs(dec) {
		if ag.Value == nil {
			continue // object literal - rejected by checkExampleArg
		}
		a.checkLiteralType("example", f, f.Type, ag.Value, ag.Pos)
	}
}

// literalTargetProblem says why `@default` or `@example`, as decName says,
// cannot give field f a literal value, or returns "": no nested array takes
// one, and a default targets only a primitive, an enum or a scalar with a
// literal form, or a single-level array of one.
func (a *analyzer) literalTargetProblem(decName string, f *ast.Field) string {
	t := f.Type
	if t == nil {
		return ""
	}
	if t.ArrayDepth > 1 {
		return fmt.Sprintf("@%s is not supported on a multi-dimensional array (field %q): it may target a primitive, scalar, enum, or a single-level array of those - not a nested array", decName, f.Name)
	}
	if decName != "default" {
		return ""
	}
	elem := t
	if t.Array {
		elem = t.ElemTypeRef()
	}
	unsupported := fmt.Sprintf("@default is not supported on field %q: only primitives, enums, scalars (wrapping primitives), and arrays of those are allowed", f.Name)
	if elem.Named == nil || elem.Named.Name == nil || len(elem.Named.Name.Parts) > 2 {
		return unsupported
	}
	rf := resolveTypeRef(elem, HasRawFormat(f.Decorators), a.pkg, a.proj)
	switch rf.Category {
	case CatEnum:
		return ""
	case CatUnknown:
		if isQualifiedTypeRef(elem) {
			return "" // a qualified name that names no type is left to the reference check
		}
	case CatPrimitive, CatScalar, CatBytes, CatRawBytes, CatFile:
		if PrimFromName(rf.ResolvedPrim) == 0 {
			break
		}
		sp, _ := prims.Lookup(rf.ResolvedPrim)
		if reason, refused := noDefault[sp.Kind]; refused {
			return fmt.Sprintf("@default is not supported on a `%s` field %q - %s", rf.ResolvedPrim, f.Name, reason)
		}
		return ""
	}
	return unsupported
}

// checkLiteralType checks literal v of decorator decName against type t:
// array shape, then enum membership or primitive kind per element.
func (a *analyzer) checkLiteralType(decName string, f *ast.Field, t *ast.TypeRef, v ast.Expr, pos lexer.Position) {
	if t == nil {
		return
	}
	if t.Array {
		arr, ok := v.(*ast.ArrayLit)
		if !ok {
			a.diag(pos, pos, lexer.SeverityError, CodeDecoratorArgType,
				"@%s on array field %q must be an array literal", decName, f.Name)
			return
		}
		elem := t.ElemTypeRef()
		for _, e := range arr.Elements {
			a.checkLiteralType(decName, f, elem, e, e.ExprPos())
		}
		return
	}
	if _, ok := v.(*ast.ArrayLit); ok {
		a.diag(pos, pos, lexer.SeverityError, CodeDecoratorArgType,
			"@%s on field %q expects a single value, not an array literal", decName, f.Name)
		return
	}
	if t.Named == nil || t.Named.Name == nil {
		return
	}
	if ed := a.lookupEnum(t.Named); ed != nil {
		a.checkEnumLiteral(decName, f.Name, ed, v, pos)
		return
	}
	a.checkPrimitiveLiteral(decName, f.Name, t.Named.Name.String(), a.primOf(t), v, pos)
}

// primitiveArgKind returns the literal kind a value of primitive prim takes,
// or ArgAny when there is none to check.
func primitiveArgKind(prim string) ArgKind {
	sp, ok := prims.Lookup(prim)
	if !ok {
		return ArgAny
	}
	switch sp.Kind {
	case prims.String, prims.Bytes, prims.DateTime:
		return ArgString
	case prims.Int, prims.Uint:
		return ArgInt
	case prims.Float:
		return ArgNumber
	case prims.Bool:
		return ArgBool
	}
	return ArgAny
}

// checkEnumLiteral checks that literal v of decorator decName names a value
// of enum ed.
func (a *analyzer) checkEnumLiteral(decName, fieldName string, ed *ast.EnumDecl, v ast.Expr, pos lexer.Position) {
	ident, ok := v.(*ast.IdentExpr)
	if !ok {
		a.diag(pos, pos, lexer.SeverityError, CodeDecoratorArgValue,
			"@%s on enum field %q must reference an enum value by name (one of %s)",
			decName, fieldName, enumValueList(ed))
		return
	}
	if ident.Name == nil || len(ident.Name.Parts) != 1 {
		a.diag(pos, pos, lexer.SeverityError, CodeDecoratorArgValue,
			"@%s on enum field %q must be one of %s", decName, fieldName, enumValueList(ed))
		return
	}
	want := ident.Name.Parts[0]
	if enumMember(ed, want) != nil {
		return
	}
	a.diag(pos, pos, lexer.SeverityError, CodeDecoratorArgValue,
		"@%s %q is not a value of enum %s; expected one of %s",
		decName, want, ed.Name, enumValueList(ed))
}

// checkPrimitiveLiteral checks that literal v of decorator decName is of the
// kind primitive prim takes, and that a `@default` number fits prim's range;
// dispName names the field's type.
func (a *analyzer) checkPrimitiveLiteral(decName, fieldName, dispName, prim string, v ast.Expr, pos lexer.Position) {
	want := primitiveArgKind(prim)
	if want == ArgAny {
		return
	}
	if !exprMatchesKind(v, want) {
		a.diag(pos, pos, lexer.SeverityError, CodeDecoratorArgType,
			"@%s on field %q (%s) requires a %s literal", decName, fieldName, dispName, want)
		return
	}
	if decName == "default" {
		if l, ok := ParseNumeric(v); ok {
			a.checkCapacity(prim, l, pos, "@default")
		}
	}
}

// enumValueList joins ed's value names with ", ".
func enumValueList(ed *ast.EnumDecl) string {
	if ed == nil {
		return ""
	}
	enumVals := ed.EnumValues()
	out := make([]string, 0, len(enumVals))
	for _, v := range enumVals {
		out = append(out, v.Name)
	}
	return strings.Join(out, ", ")
}
