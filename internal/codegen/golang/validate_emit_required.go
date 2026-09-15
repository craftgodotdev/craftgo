// Required-presence validators emitted onto the generated Validate() chain.
package golang

import (
	"fmt"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// requiredKind picks the right Go conditional for an absent value.
// The empty-string sentinel signals "this field type has no obvious
// empty value" so the caller drops the check rather than emitting a
// no-op.
//
// craftgo's "required by default" model: every non-optional field
// gets a presence check automatically. Empty strings / zero numerics
// / empty arrays / maps are allowed (pair with `@length(1, …)` /
// `@gte(1)` / `@minItems(1)` when stricter shape is required).
//
// A non-pointer scalar (`string`, `int`, `bool`, …) cannot carry
// "absent": encoding/json leaves the field at its zero value for both
// a missing key and an explicit `null`, without an error, so there is
// nothing to test at validate time. The document still lists such a
// field as required - the key IS part of the contract - which means a
// strict JSON-Schema consumer rejects input this validator accepts.
// Pair the field with `@minLength(1)` / `@gte(1)` when the zero value
// is not a legal value. For pointer types (`T?` / `T @nullable`) and
// `any` we DO need the check - the decoder accepts `null` and leaves a
// nil pointer or the literal 4-byte `null` `json.RawMessage`.
func requiredKind(f *ast.Field, access string, ctx emitCtx) string {
	if f.Type == nil {
		return ""
	}
	if f.Type.Optional || goFieldIsPointer(f, ctx.pkg, ctx.resolver) {
		return access + " == nil"
	}
	if !f.Type.Array && f.Type.Map == nil && f.Type.Named != nil {
		switch sp, _ := prims.Lookup(f.Type.Named.Name.String()); sp.Kind {
		case prims.Any, prims.JSON:
			// Bare `any` lands on Go's empty interface and bare `json`
			// on a json.RawMessage; the codec leaves either nil when the
			// key is absent. `any` also collapses an explicit JSON
			// `null` into that same nil, while `json` keeps the literal
			// four bytes - which is the point of the type, and passes
			// this check as the present value it is. The Array/Map guard
			// keeps `any[]` / `map<K,json>` on the no-check slice/map
			// path, like every other required nilable collection.
			return access + " == nil"
		}
	}
	return ""
}

// requiredCheck assembles the presence-check block, or returns ""
// when the field type doesn't have a defined empty value.
func requiredCheck(f *ast.Field, access string, ctx emitCtx) string {
	cond := requiredKind(f, access, ctx)
	if cond == "" {
		return ""
	}
	return ifReturnf(cond, fmt.Sprintf(`"%s: required"`, fieldWireName(f)), ctx)
}

// requiredCheckEnumAware adds enum support on top of `requiredCheck`. An
// enum-typed field's empty value depends on its underlying base:
// string-valued enums (and bare-value enums, which we render as
// strings) compare against `""`; int-valued enums compare against `0`.
// The check is skipped for arrays / maps / pointers - those reuse the
// generic `requiredCheck` path with len/nil semantics.
func requiredCheckEnumAware(f *ast.Field, access string, ctx emitCtx) string {
	if f != nil && f.Type != nil && !f.Type.Array && !f.Type.Optional && f.Type.Map == nil && f.Type.Named != nil {
		// A cross-package enum field gets the same field-named "required"
		// presence check a local one does, instead of only the enum's own
		// value-set rejection.
		if ed := ctx.resolver.LookupEnum(f.Type.Named.Name.String()); ed != nil {
			if semantic.EnumKind(ed) == ast.EnumInt {
				// An int-enum that defines 0 as a real member (`Inactive =
				// 0`) can't use 0 as an "absent" sentinel - the required
				// check would reject the valid member. Drop the presence
				// check rather than reject a legal value (after JSON decode
				// an absent int field and a present 0 are indistinguishable).
				if enumHasIntValue(ed, 0) {
					return ""
				}
				return ifReturnf(access+" == 0", fmt.Sprintf(`"%s: required"`, fieldWireName(f)), ctx)
			}
			// A string-enum that defines "" as a real member (`Unknown = ""`)
			// can't use "" as the "absent" sentinel either - the presence
			// check would reject that legal member before the value-set switch
			// runs. Drop it, mirroring the int-0 case above.
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

// enumHasStringValue reports whether ed defines a member whose explicit
// string value is v.
func enumHasStringValue(ed *ast.EnumDecl, v string) bool {
	for _, m := range ed.EnumValues() {
		if m.Kind == ast.EnumString && m.StrValue == v {
			return true
		}
	}
	return false
}
