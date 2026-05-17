package codegen

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// This file groups the field-shape predicates and small expression
// helpers used by every emit function. They sit one layer below the
// emitters: each takes an [ast.Field] (or [ast.TypeRef]) and answers
// "does this field qualify for validator X?" or "what's the Go-side
// access expression for this shape?".

// isStringOrOptString accepts both `string` and `string?` (optional).
// Used by validators that can sensibly skip the check when the value
// is absent - length / pattern / format. The validators handle the
// optional case by emitting a nil-guarded prefix.
func isStringOrOptString(f *ast.Field) bool {
	if f == nil || f.Type == nil || f.Type.Array || f.Type.Map != nil {
		return false
	}
	return f.Type.Named != nil && f.Type.Named.Name.String() == "string"
}

// isNumericField - non-array integer or float. Optional (`T?`) and
// `@nullable` variants are accepted; the per-validator emitters pair
// [numericValueExpr] with [optionalGuard] so the deref only runs after
// the nil-check.
func isNumericField(f *ast.Field) bool {
	if f.Type == nil || f.Type.Array || f.Type.Named == nil {
		return false
	}
	switch f.Type.Named.Name.String() {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64":
		return true
	}
	return false
}

// isIntegerField - non-array integer (signed or unsigned). Floats are
// rejected so `@multipleOf` keeps modular semantics. Optional (`T?`) and
// `@nullable` variants are accepted via the same pointer-aware path as
// [isNumericField].
func isIntegerField(f *ast.Field) bool {
	if f.Type == nil || f.Type.Array || f.Type.Named == nil {
		return false
	}
	switch f.Type.Named.Name.String() {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64":
		return true
	}
	return false
}

// isFileField reports whether the field's declared type is the DSL
// `file` builtin (rendered as `*multipart.FileHeader` in Go). Array and
// optional forms are NOT accepted - the multipart binder writes the
// pointer directly, never wraps it.
func isFileField(f *ast.Field) bool {
	if f.Type == nil || f.Type.Array || f.Type.Map != nil || f.Type.Named == nil {
		return false
	}
	return f.Type.Named.Name.String() == "file"
}

// isTypeParamRef reports whether the field's type is a direct reference
// to one of the host decl's generic parameters. Map types are excluded
// because we don't generate range-validation for them in v1.
func isTypeParamRef(t *ast.TypeRef, params []string) bool {
	if t == nil || t.Map != nil || t.Named == nil {
		return false
	}
	name := t.Named.Name.String()
	for _, p := range params {
		if p == name {
			return true
		}
	}
	return false
}

// isComparableElem reports whether an element type can be used as a map
// key in the generated uniqueness loop. Slices, maps, and func types are
// not comparable and would fail to compile.
func isComparableElem(elem string) bool {
	if elem == "" {
		return false
	}
	if strings.HasPrefix(elem, "[]") || strings.HasPrefix(elem, "map[") || strings.HasPrefix(elem, "func") {
		return false
	}
	return true
}

// arrayElemType returns the Go type of the array's element (without the
// `[]` prefix). Optional / map element types fall back to `any` so the
// generated code compiles even though the uniqueness check is skipped.
func arrayElemType(t *ast.TypeRef) string {
	if t == nil || !t.Array {
		return ""
	}
	clone := *t
	if clone.ArrayDepth > 0 {
		clone.ArrayDepth--
	}
	if clone.ArrayDepth == 0 {
		clone.Array = false
	}
	clone.Optional = false
	return GoTypeRef(&clone)
}

// optionalGuard returns the leading nil-check expression for any
// field whose generated Go type is a pointer (`*T`). Plain value
// fields return the empty string - their access expression is already
// a concrete value. Both `T?` (optional) and `T @nullable` (forced
// pointer to allow JSON null) end up as Go pointers, so the same
// guard handles both.
func optionalGuard(f *ast.Field, access string) string {
	if goFieldIsPointer(f) {
		return access + " != nil && "
	}
	return ""
}

// stringValueExpr returns the string-typed access expression. Pointer
// fields (`T?` or `@nullable T`) get a single dereference; plain fields
// pass through. Pair with [optionalGuard] so the dereference only
// runs after the nil check.
func stringValueExpr(f *ast.Field, access string) string {
	if goFieldIsPointer(f) {
		return "*" + access
	}
	return access
}
