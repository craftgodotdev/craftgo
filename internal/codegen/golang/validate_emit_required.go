package golang

import (
	"fmt"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// requiredKind returns f's absent condition (a nil pointer or `any`, an empty
// raw field), or "" for any other type, whose missing key decodes to its zero value.
func requiredKind(f *ast.Field, access string, ctx emitCtx) string {
	if f.Type == nil {
		return ""
	}
	if f.Type.Optional || goFieldIsPointer(f, ctx.pkg, ctx.resolver) {
		return access + " == nil"
	}
	if isRawBytesField(f, ctx.pkg, ctx.resolver) {
		// An explicit `null` decodes to the bytes `null`, so only a missing key is empty.
		return "len(" + access + ") == 0"
	}
	if !f.Type.Array && f.Type.Map == nil && f.Type.Named != nil {
		switch sp, _ := prims.Lookup(f.Type.Named.Name.String()); sp.Kind {
		case prims.Any:
			return access + " == nil"
		}
	}
	return ""
}

// requiredCheck renders f's required check, or "" when [requiredKind] has none.
func requiredCheck(f *ast.Field, access string, ctx emitCtx) string {
	cond := requiredKind(f, access, ctx)
	if cond == "" {
		return ""
	}
	return ifReturnf(cond, fmt.Sprintf(`"%s: required"`, fieldWireName(f)), ctx)
}

// requiredCheckEnumAware is [requiredCheck] that also covers a flat enum field,
// absent at its zero value unless that value is one of the enum's members.
func requiredCheckEnumAware(f *ast.Field, access string, ctx emitCtx) string {
	if f != nil && f.Type != nil && !f.Type.Array && !f.Type.Optional && f.Type.Map == nil && f.Type.Named != nil {
		if ed := ctx.resolver.LookupEnum(f.Type.Named.Name.String()); ed != nil {
			if semantic.EnumKind(ed) == ast.EnumInt {
				if enumHasIntValue(ed, 0) {
					return ""
				}
				return ifReturnf(access+" == 0", fmt.Sprintf(`"%s: required"`, fieldWireName(f)), ctx)
			}
			if enumHasStringValue(ed, "") {
				return ""
			}
			return ifReturnf(access+` == ""`, fmt.Sprintf(`"%s: required"`, fieldWireName(f)), ctx)
		}
	}
	return requiredCheck(f, access, ctx)
}

// enumHasIntValue reports whether ed defines a member whose int value is v.
func enumHasIntValue(ed *ast.EnumDecl, v int64) bool {
	for _, m := range ed.EnumValues() {
		if m.Kind == ast.EnumInt && m.IntValue == v {
			return true
		}
	}
	return false
}

// enumHasStringValue reports whether ed defines a member whose explicit string value is v.
func enumHasStringValue(ed *ast.EnumDecl, v string) bool {
	for _, m := range ed.EnumValues() {
		if m.Kind == ast.EnumString && m.StrValue == v {
			return true
		}
	}
	return false
}
