package semantic

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

func (a *analyzer) checkFieldDefault(f *ast.Field) {
	if f == nil {
		return
	}
	dec := ast.FindDecorator(f.Decorators, "default")
	if dec == nil {
		return
	}
	// A nested array is refused whatever its element, cross-package included.
	if f.Type != nil && f.Type.ArrayDepth > 1 {
		a.diag(dec.Pos, decoratorEnd(dec), lexer.SeverityError, CodeDecoratorConflict,
			"@default is not supported on a multi-dimensional array (field %q): a default may target a primitive, scalar, enum, or a single-level array of those - not a nested array",
			f.Name)
		return
	}
	if !a.defaultTypeSupported(f.Type) {
		a.diag(dec.Pos, decoratorEnd(dec), lexer.SeverityError,
			CodeDecoratorConflict,
			"@default is not supported on field %q: only primitives, enums, scalars (wrapping primitives), and arrays of those are allowed",
			f.Name)
		return
	}
	// A @path segment is always present, so it needs no `?`.
	if f.Type != nil && !f.Type.Optional && !ast.HasDecorator(f.Decorators, wire.BindingPath) {
		a.diag(dec.Pos, decoratorEnd(dec), lexer.SeverityWarning, CodeDefaultNeedsOptional,
			"@default on non-optional field %q: the default fires when the value is absent, so the field is optional - add `?` (or run `craftgo fmt`) so types.go, validate.go, and the OpenAPI agree it is optional",
			f.Name)
	}
	pos := positionalArgs(dec)
	if len(pos) != 1 {
		return
	}
	a.checkDefaultLiteral(f, f.Type, pos[0].Value, pos[0].Pos)
}

// checkFieldExample checks an `@example` literal against f's type as a
// `@default` is checked.
func (a *analyzer) checkFieldExample(f *ast.Field) {
	if f == nil {
		return
	}
	dec := ast.FindDecorator(f.Decorators, "example")
	if dec == nil {
		return
	}
	if f.Type != nil && f.Type.ArrayDepth > 1 {
		a.diag(dec.Pos, decoratorEnd(dec), lexer.SeverityError, CodeDecoratorConflict,
			"@example is not supported on a multi-dimensional array (field %q): an example may target a primitive, scalar, enum, or a single-level array of those - not a nested array",
			f.Name)
		return
	}
	for _, ag := range positionalArgs(dec) {
		if ag.Value == nil {
			continue // object literal - rejected by checkExampleArg
		}
		a.checkLiteralType("example", f, f.Type, ag.Value, ag.Pos)
	}
}

// checkDefaultLiteral checks a `@default` literal against type t.
func (a *analyzer) checkDefaultLiteral(f *ast.Field, t *ast.TypeRef, v ast.Expr, pos lexer.Position) {
	a.checkLiteralType("default", f, t, v, pos)
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
	a.checkScalarEnumLiteralValue(decName, f.Name, t.Named.Name.String(), a.primOf(t), a.lookupEnum(t.Named), v, pos)
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

// checkScalarEnumLiteralValue checks one non-array literal against enum ed
// or, when ed is nil, primitive prim; some checks apply to `@default` only.
func (a *analyzer) checkScalarEnumLiteralValue(decName, fieldName, dispName, prim string, ed *ast.EnumDecl, v ast.Expr, pos lexer.Position) {
	if ed != nil {
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
		for _, ev := range ed.EnumValues() {
			if ev.Name == want {
				return
			}
		}
		a.diag(pos, pos, lexer.SeverityError, CodeDecoratorArgValue,
			"@%s %q is not a value of enum %s; expected one of %s",
			decName, want, ed.Name, enumValueList(ed))
		return
	}
	if decName == "default" {
		if prim == "bytes" {
			a.diag(pos, pos, lexer.SeverityError, CodeDecoratorConflict,
				"@default is not supported on a `bytes` field %q - a bytes value has no unambiguous literal form (Go []byte vs OpenAPI base64 `format: byte`)",
				fieldName)
			return
		}
		if prim == "file" {
			a.diag(pos, pos, lexer.SeverityError, CodeDecoratorConflict,
				"@default is not supported on a `file` field %q - a file upload has no literal default form",
				fieldName)
			return
		}
		if prim == "datetime" {
			a.diag(pos, pos, lexer.SeverityError, CodeDecoratorConflict,
				"@default is not supported on a `datetime` field %q - a fixed timestamp is rarely the default meant, and \"now\" is the handler's to decide",
				fieldName)
			return
		}
	}
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
		if il, ok := v.(*ast.IntLit); ok {
			if lo, hi, capOK := prims.Capacity(prim); capOK {
				fv := float64(il.Value)
				if fv < lo || fv > hi {
					a.diag(pos, pos, lexer.SeverityError, CodeBoundOverflow,
						"@default %d is out of range for %s [%g, %g]", il.Value, prim, lo, hi)
				}
			}
		}
	}
}

// defaultTypeSupported reports whether `@default` may target type t: a
// primitive, an enum, a scalar over a primitive, or an array of one.
func (a *analyzer) defaultTypeSupported(t *ast.TypeRef) bool {
	if t == nil || t.Map != nil {
		return false
	}
	if t.Array {
		return a.defaultElemSupported(t.ElemTypeRef())
	}
	return a.defaultElemSupported(t)
}

// defaultElemSupported is defaultTypeSupported for a non-array type; a
// qualified name that names no type passes, for the reference check to report.
func (a *analyzer) defaultElemSupported(t *ast.TypeRef) bool {
	if t == nil || t.Named == nil || t.Named.Name == nil || len(t.Named.Name.Parts) > 2 {
		return false
	}
	if len(t.Named.Name.Parts) == 1 && PrimFromName(t.Named.Name.Parts[0]) != 0 {
		return true
	}
	if a.lookupEnum(t.Named) != nil {
		return true
	}
	if sd := a.lookupScalar(t.Named); sd != nil {
		return PrimFromName(sd.Primitive) != 0
	}
	if isQualifiedTypeRef(t) {
		pkg, sym := a.proj.resolve(a.pkg.Name, t.Named.Name)
		return pkg == nil || pkg.Decl(sym, TypeRefDecls) == nil
	}
	return false
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
