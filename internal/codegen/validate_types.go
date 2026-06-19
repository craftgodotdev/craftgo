package codegen

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
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

// isLengthCheckable accepts the two len()-checkable primitives - `string`
// and `bytes` (Go []byte) - for the length validators (@length /
// @minLength / @maxLength). `@pattern` / `@format` stay string-only via
// [isStringOrOptString]; a byte slice has no sensible regexp / format.
func isLengthCheckable(f *ast.Field) bool {
	if f == nil || f.Type == nil || f.Type.Array || f.Type.Map != nil || f.Type.Named == nil {
		return false
	}
	switch f.Type.Named.Name.String() {
	case "string", "bytes":
		return true
	}
	return false
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
// `file` builtin (rendered as `*multipart.FileHeader` in Go). Array
// and map shapes are NOT accepted - the multipart binder writes the
// pointer directly, never wraps it. Optional `file?` IS accepted: the
// Go type is `*multipart.FileHeader` either way (the type renderer
// skips the pointer wrap for already-nilable types via
// [isNilableGoType]) so `file?` and `file` produce identical wire
// code; the `?` documents author intent without changing semantics.
func isFileField(f *ast.Field) bool {
	if f.Type == nil || f.Type.Array || f.Type.Map != nil || f.Type.Named == nil {
		return false
	}
	return f.Type.Named.Name.String() == "file"
}

// isTypeParamRef reports whether the field's type is a direct
// reference to one of the host decl's generic parameters. Map types
// are excluded because range-validation for map values isn't
// generated yet.
func isTypeParamRef(t *ast.TypeRef, params []string) bool {
	if t == nil {
		return false
	}
	// A map whose VALUE position references a type parameter
	// (`map<K, T>`, `map<K, T[]>`) still needs its values validated: the
	// type-param dispatch's validateValue fallback walks the map
	// reflectively. Without recognising this, the field was dropped from
	// the validator entirely while OpenAPI advertised the value-type
	// constraints.
	if t.Map != nil {
		return isTypeParamRef(t.Map.Value, params)
	}
	if t.Named == nil {
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

// optionalGuard returns the leading nil-check expression for any field
// whose access can legitimately be nil. Plain value fields return the
// empty string - their access is already a concrete value. Both `T?`
// (optional) and `T @nullable` (forced pointer) end up as Go pointers
// and are guarded; so is a nilable-typed field (bytes / slice / map)
// that is optional or `@nullable` - it carries no extra `*`, but a nil
// value is the valid "absent / null" state, so the constraint must
// skip it rather than run `len(nil)` and reject what the OpenAPI
// null-union advertises as legal.
func optionalGuard(f *ast.Field, access string) string {
	// optionalGuard runs only on raw value-shaped fields (string / numeric /
	// bytes / slice / map); a named scalar's field-level checks route
	// through [scalarFieldLevelChecks], which owns its own guard. So the
	// nilability question here is answered by [isNilableGoType] alone and
	// no scalar resolver is needed.
	if fieldNeedsNilGuard(f, nil, nil) {
		return access + " != nil && "
	}
	return ""
}

// fieldNeedsNilGuard reports whether f's value can be nil in a state the
// contract treats as valid (absent / null), so a constraint check must
// nil-guard first. True for any pointer field, and for a nilable Go type
// - bytes / slice / map, OR a scalar whose underlying primitive is nilable
// (`scalar Blob bytes`) - marked optional (`?`) or `@nullable`.
func fieldNeedsNilGuard(f *ast.Field, pkg *semantic.Package, r *ProjectResolver) bool {
	if goFieldIsPointer(f, pkg, r) {
		return true
	}
	if f == nil || f.Type == nil {
		return false
	}
	if !f.Type.Optional && !hasNullableDecorator(f.Decorators) {
		return false
	}
	return isNilableGoType(GoTypeRef(f.Type)) || scalarRefNilable(f.Type, pkg, r)
}

// stringValueExpr returns the string-typed access expression. Pointer
// fields (`T?` or `@nullable T`) get a single dereference; plain fields
// pass through. Pair with [optionalGuard] so the dereference only
// runs after the nil check. Only string-typed fields reach here, never a
// nilable scalar, so the pointer test needs no scalar resolver.
func stringValueExpr(f *ast.Field, access string) string {
	if goFieldIsPointer(f, nil, nil) {
		return "*" + access
	}
	return access
}
