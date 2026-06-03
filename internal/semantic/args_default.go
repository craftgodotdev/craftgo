// Package semantic — @default literal validation: type/element support,
// primitive-kind map, helpers.
package semantic

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
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
			"@default is not supported on a multi-dimensional array (field %q): a default may target a primitive, scalar, enum, or a single-level array of those — not a nested array",
			f.Name)
		return
	}
	if !defaultTypeSupported(f.Type, a.pkg) {
		a.diag(dec.Pos, decoratorEnd(dec), lexer.SeverityError,
			CodeDecoratorConflict,
			"@default is not supported on field %q: only primitives, enums, scalars (wrapping primitives), and arrays of those are allowed",
			f.Name)
		return
	}
	pos := positionalArgs(dec)
	if len(pos) != 1 {
		return
	}
	a.checkDefaultLiteral(f, f.Type, pos[0].Value, pos[0].Pos)
}

// checkDefaultLiteral validates the literal arg against a resolved
// type. Recurses through arrays so `@default([Active, Pending])` on a
// `Status[]` field flags any non-IdentExpr element or unknown enum
// value. For primitive / scalar fields the literal kind must match
// the resolved primitive (string vs int vs bool, ...).
func (a *analyzer) checkDefaultLiteral(f *ast.Field, t *ast.TypeRef, v ast.Expr, pos lexer.Position) {
	if t == nil {
		return
	}
	if t.Array {
		arr, ok := v.(*ast.ArrayLit)
		if !ok {
			a.diag(pos, pos, lexer.SeverityError, CodeDecoratorArgType,
				"@default on array field %q must be an array literal", f.Name)
			return
		}
		elem := arrayElemTypeRef(t)
		for _, e := range arr.Elements {
			a.checkDefaultLiteral(f, elem, e, e.ExprPos())
		}
		return
	}
	if _, ok := v.(*ast.ArrayLit); ok {
		a.diag(pos, pos, lexer.SeverityError, CodeDecoratorArgType,
			"@default on field %q expects a single value, not an array literal", f.Name)
		return
	}
	if t.Named == nil || t.Named.Name == nil || len(t.Named.Name.Parts) != 1 {
		return
	}
	name := t.Named.Name.Parts[0]
	if ed, isEnum := a.pkg.Enums[name]; isEnum {
		ident, ok := v.(*ast.IdentExpr)
		if !ok {
			a.diag(pos, pos, lexer.SeverityError, CodeDecoratorArgValue,
				"@default on enum field %q must reference an enum value by name (one of %s)",
				f.Name, enumValueList(ed))
			return
		}
		if ident.Name == nil || len(ident.Name.Parts) != 1 {
			a.diag(pos, pos, lexer.SeverityError, CodeDecoratorArgValue,
				"@default on enum field %q must be one of %s", f.Name, enumValueList(ed))
			return
		}
		want := ident.Name.Parts[0]
		for _, ev := range ed.EnumValues() {
			if ev.Name == want {
				return
			}
		}
		a.diag(pos, pos, lexer.SeverityError, CodeDecoratorArgValue,
			"@default %q is not a value of enum %s; expected one of %s",
			want, ed.Name, enumValueList(ed))
		return
	}
	// Resolve a scalar to its underlying primitive for the type-fit checks.
	prim := name
	if sd, ok := a.pkg.Scalars[name]; ok {
		prim = sd.Primitive
	}
	// A `bytes` field has no unambiguous literal default: the Go side needs
	// `[]byte(...)` while the OpenAPI `format: byte` default is base64, and
	// the only literal kind the gate accepts (string) miscompiles straight
	// into the []byte slot. Reject rather than emit non-compiling Go.
	if prim == "bytes" {
		a.diag(pos, pos, lexer.SeverityError, CodeDecoratorConflict,
			"@default is not supported on a `bytes` field %q — a bytes value has no unambiguous literal form (Go []byte vs OpenAPI base64 `format: byte`)",
			f.Name)
		return
	}
	// A `file` upload (`*multipart.FileHeader`) has no literal default form;
	// a string/int literal would be assigned into the pointer field and not
	// compile. Reject like `bytes`.
	if prim == "file" {
		a.diag(pos, pos, lexer.SeverityError, CodeDecoratorConflict,
			"@default is not supported on a `file` field %q — a file upload has no literal default form",
			f.Name)
		return
	}
	want := defaultPrimitiveKind(name, a.pkg)
	if want == ArgAny {
		return
	}
	if !exprMatchesKind(v, want) {
		a.diag(pos, pos, lexer.SeverityError, CodeDecoratorArgType,
			"@default on field %q (%s) requires a %s literal", f.Name, name, want)
		return
	}
	// An integer default outside the field primitive's capacity emits a
	// non-compiling cast (`uint(-5)`, `int8(200)`). Mirror the bound
	// capacity guard so the prefill compiles. Floats are skipped — the
	// codegen cast holds every literal the kind check accepts.
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

// defaultPrimitiveKind maps a resolved primitive (or scalar) name to
// the [ArgKind] its `@default` literal must match. Scalars resolve
// through to their underlying primitive in one hop. Returns ArgAny
// for names this layer can't classify so the caller skips the kind
// check rather than emit a misleading mismatch.
func defaultPrimitiveKind(name string, pkg *Package) ArgKind {
	if sd, ok := pkg.Scalars[name]; ok {
		name = sd.Primitive
	}
	switch name {
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

// defaultTypeSupported reports whether @default may target a field of
// type t. Path C: primitives, enums, scalars wrapping primitives,
// optional of those, and arrays of those are allowed. Map / struct /
// generic / array-of-struct return false so the caller can flag the
// combination. Cross-package qualified refs (multi-segment names)
// DEFER — they return true at per-package phase and are re-validated
// by [refResolver.checkProjectFieldDefaults] with the project-wide
// scalar / enum tables in scope.
func defaultTypeSupported(t *ast.TypeRef, pkg *semanticPkgRef) bool {
	if t == nil || t.Map != nil {
		return false
	}
	if t.Array {
		return defaultElemSupported(arrayElemTypeRef(t), pkg)
	}
	return defaultElemSupported(t, pkg)
}

// defaultElemSupported is the per-element check used both for
// stand-alone fields and array elements.
func defaultElemSupported(t *ast.TypeRef, pkg *semanticPkgRef) bool {
	if t == nil || t.Named == nil || t.Named.Name == nil {
		return false
	}
	if len(t.Named.Name.Parts) == 2 {
		// Qualified ref (`shared.CurrencyCode`). The per-package
		// analyser has no cross-package view, so we defer to
		// [refResolver.checkProjectFieldDefaults] which runs after
		// every package is built and has access to the full scalar
		// / enum tables.
		return true
	}
	if len(t.Named.Name.Parts) != 1 {
		return false
	}
	name := t.Named.Name.Parts[0]
	if PrimFromName(name) != 0 {
		return true
	}
	if _, ok := pkg.Enums[name]; ok {
		return true
	}
	if sd, ok := pkg.Scalars[name]; ok {
		return PrimFromName(sd.Primitive) != 0
	}
	return false
}

// semanticPkgRef is the alias [defaultTypeSupported] takes for its
// package-table argument. Kept as a named alias (not the bare
// `*Package`) so the call sites read as "this helper needs only a
// scalar / enum table" rather than the full analyzer state.
type semanticPkgRef = Package

// arrayElemTypeRef returns the element TypeRef of an array. The stored
// TypeRef has Array == true alongside the element's Named / Optional
// fields, so dropping the Array flag yields the element type. Optional
// propagates so `T?[]` element is `T?`. A multi-dimensional array default
// is rejected up front (see [analyzer.checkFieldDefault]), so this only
// ever peels a single-level array.
func arrayElemTypeRef(t *ast.TypeRef) *ast.TypeRef {
	if t == nil {
		return nil
	}
	clone := *t
	clone.Array = false
	return &clone
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
