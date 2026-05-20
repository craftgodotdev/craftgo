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
	if f.Type.Optional || goFieldIsPointer(f) {
		return access + " == nil"
	}
	if f.Type.Named != nil && f.Type.Named.Name.String() == "any" {
		// `any` lands on Go's empty interface; the codec leaves it
		// nil for absent fields and for explicit JSON `null` (the
		// decoder collapses both into the zero interface value).
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
	return ifReturnf(cond, fmt.Sprintf(`"%s: required"`, f.Name))
}

// requiredCheckEnumAware adds enum support on top of `requiredCheck`. An
// enum-typed field's empty value depends on its underlying base:
// string-valued enums (and bare-value enums, which we render as
// strings) compare against `""`; int-valued enums compare against `0`.
// The check is skipped for arrays / maps / pointers - those reuse the
// generic `requiredCheck` path with len/nil semantics.
func requiredCheckEnumAware(f *ast.Field, access string, pkg *semantic.Package, uses map[string]bool) string {
	if f != nil && f.Type != nil && !f.Type.Array && !f.Type.Optional && f.Type.Map == nil && f.Type.Named != nil {
		if ed, ok := pkg.Enums[f.Type.Named.Name.String()]; ok {
			cond := access + ` == ""`
			if firstEnumKind(ed) == ast.EnumInt {
				cond = access + " == 0"
			}
			uses["fmt"] = true
			return ifReturnf(cond, fmt.Sprintf(`"%s: required"`, f.Name))
		}
	}
	return requiredCheck(f, access, uses)
}
