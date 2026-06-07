// Required-presence validators emitted onto the generated Validate() chain.
package codegen

import (
	"fmt"

	"github.com/craftgodotdev/craftgo/internal/ast"
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
// For non-pointer scalar types (`string`, `int`, `bool`, …) the
// JSON decoder already rejects wire `null` with an unmarshal error,
// so no validate-time check is needed; the diagnostic surfaces at
// the framework boundary instead. For pointer types (`T?` /
// `T @nullable`) and `any` we DO need the check - the decoder
// happily accepts `null` and leaves it as a nil pointer or the
// literal 4-byte `null` `json.RawMessage`.
func requiredKind(f *ast.Field, access string) string {
	if f.Type == nil {
		return ""
	}
	if f.Type.Optional || goFieldIsPointer(f, nil, nil) {
		// requiredKind runs only on required (non-optional, non-nullable)
		// fields, where the pointer wrap never applies — so a scalar
		// resolver would not change the answer here.
		return access + " == nil"
	}
	if !f.Type.Array && f.Type.Map == nil && f.Type.Named != nil && f.Type.Named.Name.String() == "any" {
		// Bare `any` lands on Go's empty interface; the codec leaves it
		// nil for absent fields and for explicit JSON `null` (the
		// decoder collapses both into the zero interface value). The
		// Array/Map guard keeps `any[]` / `map<K,any>` on the no-check
		// slice/map path, like every other required nilable collection.
		return access + " == nil"
	}
	return ""
}

// requiredCheck assembles the presence-check block, or returns ""
// when the field type doesn't have a defined empty value.
func requiredCheck(f *ast.Field, access string, uses map[string]bool) string {
	cond := requiredKind(f, access)
	if cond == "" {
		return ""
	}
	uses["fmt"] = true
	return ifReturnf(cond, fmt.Sprintf(`"%s: required"`, fieldWireName(f)))
}

// requiredCheckEnumAware adds enum support on top of `requiredCheck`. An
// enum-typed field's empty value depends on its underlying base:
// string-valued enums (and bare-value enums, which we render as
// strings) compare against `""`; int-valued enums compare against `0`.
// The check is skipped for arrays / maps / pointers - those reuse the
// generic `requiredCheck` path with len/nil semantics.
func requiredCheckEnumAware(f *ast.Field, access string, pkg *semantic.Package, r *ProjectResolver, uses map[string]bool) string {
	if f != nil && f.Type != nil && !f.Type.Array && !f.Type.Optional && f.Type.Map == nil && f.Type.Named != nil {
		name := f.Type.Named.Name.String()
		ed, ok := pkg.Enums[name]
		if !ok && r != nil {
			// A qualified enum (`shared.Priority`) misses the bare-keyed local
			// table; resolve it project-wide so a cross-package enum field gets
			// the same field-named "required" presence check a local one does,
			// instead of only the enum's own value-set rejection.
			if ed = r.LookupEnum(name); ed != nil {
				ok = true
			}
		}
		if ok {
			if firstEnumKind(ed) == ast.EnumInt {
				// An int-enum that defines 0 as a real member (`Inactive =
				// 0`) can't use 0 as an "absent" sentinel — the required
				// check would reject the valid member. Drop the presence
				// check rather than reject a legal value (after JSON decode
				// an absent int field and a present 0 are indistinguishable).
				if enumHasIntValue(ed, 0) {
					return ""
				}
				uses["fmt"] = true
				return ifReturnf(access+" == 0", fmt.Sprintf(`"%s: required"`, fieldWireName(f)))
			}
			// A string-enum that defines "" as a real member (`Unknown = ""`)
			// can't use "" as the "absent" sentinel either — the presence
			// check would reject that legal member before the value-set switch
			// runs. Drop it, mirroring the int-0 case above.
			if enumHasStringValue(ed, "") {
				return ""
			}
			uses["fmt"] = true
			return ifReturnf(access+` == ""`, fmt.Sprintf(`"%s: required"`, fieldWireName(f)))
		}
	}
	return requiredCheck(f, access, uses)
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
