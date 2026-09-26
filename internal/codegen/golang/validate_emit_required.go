package golang

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// requiredCheck renders the presence check of required field rf held in t: an
// enum is absent at its zero value unless that value is one of its members, a
// file or `any` at nil, a raw field when empty. Any other type gets none: its
// missing key decodes to its zero value.
func requiredCheck(rf semantic.ResolvedField, t checkTarget, ctx emitCtx) string {
	var cond string
	switch rf.Category {
	case semantic.CatEnum:
		if !enumHasZeroMember(ctx.resolver.LookupEnum(rf.Field.Type.Named.Name.String())) {
			zero := `""`
			if rf.ResolvedPrim == "int" {
				zero = "0"
			}
			cond = t.access + " == " + zero
		}
	case semantic.CatFile, semantic.CatAny:
		cond = t.access + " == nil"
	case semantic.CatRawBytes:
		// An explicit `null` decodes to the bytes `null`, so only a missing key is empty.
		cond = "len(" + t.access + ") == 0"
	}
	if cond == "" {
		return ""
	}
	return failIf(cond, t.subject, "required", ctx)
}

// typeParamPresence renders the presence check of a required type-parameter
// value held in t, through the absentValue helper: a `file` or `any`
// argument is absent at nil.
func typeParamPresence(t checkTarget, ctx emitCtx) string {
	ctx.imports.use("mime/multipart")
	ctx.helpers.absentValue = true
	return failIf("absentValue(&"+t.access+")", t.subject, "required", ctx)
}

// enumHasZeroMember reports whether one of ed's members has its kind's zero
// wire value, 0 or "".
func enumHasZeroMember(ed *ast.EnumDecl) bool {
	for _, m := range ed.EnumValues() {
		switch w := semantic.EnumMemberWire(m).(type) {
		case int64:
			if w == 0 {
				return true
			}
		case string:
			if w == "" {
				return true
			}
		}
	}
	return false
}
