package golang

import (
	"slices"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/prims"
)

// isStringOrOptString reports whether f is a flat `string` field, optional or not.
func isStringOrOptString(f *ast.Field) bool {
	if f == nil || f.Type == nil || f.Type.Array || f.Type.Map != nil {
		return false
	}
	return f.Type.Named != nil && f.Type.Named.Name.String() == "string"
}

// isLengthCheckable reports whether f is a flat `string` or `bytes` field.
func isLengthCheckable(f *ast.Field) bool {
	if f == nil || f.Type == nil || f.Type.Array || f.Type.Map != nil || f.Type.Named == nil {
		return false
	}
	switch sp, _ := prims.Lookup(f.Type.Named.Name.String()); sp.Kind {
	case prims.String, prims.Bytes:
		return true
	}
	return false
}

// isNumericField reports whether f is a non-array integer or float field,
// optional or not.
func isNumericField(f *ast.Field) bool {
	if f.Type == nil || f.Type.Array || f.Type.Named == nil {
		return false
	}
	return prims.IsNumeric(f.Type.Named.Name.String())
}

// isIntegerField reports whether f is a non-array integer field, optional or not.
func isIntegerField(f *ast.Field) bool {
	if f.Type == nil || f.Type.Array || f.Type.Named == nil {
		return false
	}
	return prims.IsInteger(f.Type.Named.Name.String())
}

// isFileField reports whether f is a flat `file` field; `file` and `file?` are
// both `*multipart.FileHeader`.
func isFileField(f *ast.Field) bool {
	if f.Type == nil || f.Type.Array || f.Type.Map != nil || f.Type.Named == nil {
		return false
	}
	return f.Type.Named.Name.String() == "file"
}

// isTypeParamRef reports whether t, or the value type of a map t, names one of
// params.
func isTypeParamRef(t *ast.TypeRef, params []string) bool {
	if t == nil {
		return false
	}
	if t.Map != nil {
		return isTypeParamRef(t.Map.Value, params)
	}
	if t.Named == nil {
		return false
	}
	return slices.Contains(params, t.Named.Name.String())
}

// isComparableElem reports whether Go element type elem can key the dedupe map.
func isComparableElem(elem string) bool {
	if elem == "" {
		return false
	}
	if strings.HasPrefix(elem, "[]") || strings.HasPrefix(elem, "map[") || strings.HasPrefix(elem, "func") {
		return false
	}
	return true
}

// arrayElemType returns the Go type of one element of array t, or "" when t is
// not an array.
func arrayElemType(t *ast.TypeRef) string {
	if t == nil || !t.Array {
		return ""
	}
	return goTypeRef(t.ElemTypeRef())
}

// optionalGuard returns the `access != nil && ` prefix for an optional or
// @nullable field, whose nil is the valid absent/null value.
func optionalGuard(f *ast.Field, access string) string {
	if fieldNeedsNilGuard(f) {
		return access + " != nil && "
	}
	return ""
}

// stringValueExpr dereferences access when f is a pointer; pair it with
// [optionalGuard].
func stringValueExpr(f *ast.Field, access string, ctx emitCtx) string {
	if goFieldIsPointer(f, ctx.pkg, ctx.resolver) {
		return "*" + access
	}
	return access
}
