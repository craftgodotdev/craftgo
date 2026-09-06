// @default / @example literal validation: target type support, literal
// kind and value fit.
package semantic

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
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
	// @default targets a primitive / scalar / enum or a SINGLE-level array
	// of those. A multi-dimensional array default has no real use and its
	// nested-literal form is a sharp edge, so it is rejected outright. The
	// check is structural (array depth, independent of the element type), so
	// it fires for cross-package element types too.
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
	// @default on a non-optional, non-@path field: the default fires when the
	// value is absent, so the field is conceptually optional. Warn (the docs
	// promise this, and `craftgo fmt` auto-adds `?`); a @path segment is always
	// present, so it is exempt.
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

// checkFieldExample type-checks an `@example` literal against the field's
// type, reusing the SAME validator as `@default` (checkLiteralType) so the
// two agree - a string example on an int field, or a non-member value on an
// enum field, is rejected just as the equivalent default is. Object-literal
// args are left to [checkExampleArg]. Without this, @example silently
// emitted spec examples that contradicted their own schema.
func (a *analyzer) checkFieldExample(f *ast.Field) {
	if f == nil {
		return
	}
	dec := ast.FindDecorator(f.Decorators, "example")
	if dec == nil {
		return
	}
	// A multi-dimensional array has no single-value example shape and its
	// nested-literal form is the same sharp edge @default rejects - reject up
	// front with the structural message, rather than letting the per-element
	// walk misreport the inner array as "expects a single value". Mirrors
	// [analyzer.checkFieldDefault].
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

// checkDefaultLiteral validates a `@default` literal against the field's
// resolved type. Thin wrapper over [checkLiteralType] - the value-vs-type
// logic is shared with `@example` so the two decorators agree on what a
// valid literal is; the `@default`-specific rejects (bytes / file / int
// capacity) ride inside, gated on the decorator name.
func (a *analyzer) checkDefaultLiteral(f *ast.Field, t *ast.TypeRef, v ast.Expr, pos lexer.Position) {
	a.checkLiteralType("default", f, t, v, pos)
}

// checkLiteralType validates a value-bearing decorator's literal against a
// resolved type: array shape, enum membership, and primitive-kind fit.
// Recurses through arrays so `[Active, Pending]` on a `Status[]` field flags
// any non-member element. Shared by `@default` and `@example` (decName) so a
// string example on an int field is rejected exactly like a string default
// is. The rejects that are meaningful ONLY for a prefilled default
// (bytes/file have no literal default form; an out-of-capacity int would not
// compile) are gated on decName == "default".
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

// primitiveArgKind maps a resolved primitive name to the literal kind a
// value-bearing decorator must carry. Unknown names (structs, unresolved
// refs) return ArgAny so no kind check fires.
func primitiveArgKind(prim string) ArgKind {
	switch prim {
	case "string", "bytes":
		return ArgString
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64":
		return ArgInt
	case "float32", "float64":
		return ArgNumber
	case "bool":
		return ArgBool
	}
	return ArgAny
}

// checkScalarEnumLiteralValue validates one non-array literal against an
// already-resolved enum (ed != nil) OR a resolved scalar/primitive (prim).
// dispName is the type name used in messages. decName gates the default-only
// rejects (bytes/file have no literal form; an out-of-capacity int would not
// compile). A `shared.Tiny @default(200)` gets the same kind / capacity /
// membership verdict as a local `Tiny @default(200)`.
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
			if lo, hi, capOK := intCapacity(prim); capOK {
				fv := float64(il.Value)
				if fv < lo || fv > hi {
					a.diag(pos, pos, lexer.SeverityError, CodeBoundOverflow,
						"@default %d is out of range for %s [%g, %g]", il.Value, prim, lo, hi)
				}
			}
		}
	}
}

// defaultTypeSupported reports whether @default may target a field of
// type t: primitives, enums, scalars wrapping primitives, optional of
// those, and arrays of those. Map / struct / generic / array-of-struct
// return false so the caller can flag the combination.
func (a *analyzer) defaultTypeSupported(t *ast.TypeRef) bool {
	if t == nil || t.Map != nil {
		return false
	}
	if t.Array {
		return a.defaultElemSupported(t.ElemTypeRef())
	}
	return a.defaultElemSupported(t)
}

// defaultElemSupported is the per-element check used both for
// stand-alone fields and array elements. A qualified name that resolves
// to nothing is left to the reference pass.
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
		pkg, sym := a.resolveNamed(a.pkg.Name, t.Named)
		return pkg == nil || !packageHasSymbol(pkg, sym)
	}
	return false
}

// enumValueList renders an enum's value names as a comma-separated
// list for diagnostic messages.
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
