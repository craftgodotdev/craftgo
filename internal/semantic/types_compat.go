package semantic

// Field-type compatibility check for validator decorators. A `@length`
// on an int field, or `@uniqueItems` on a string, is almost always a
// bug - the README's compatibility matrix groups validators by
// primitive category, and we surface the mismatch as a clear
// [CodeDecoratorTypeMismatch] diagnostic rather than letting codegen
// silently drop the validator.
//
// The check resolves a field's primitive category by:
//
//   1. Inspecting the AST [ast.TypeRef] modifiers: `T[]` and `map<K,V>`
//      collapse to PrimArray.
//   2. Looking up the named type - built-in primitives map directly;
//      custom scalars are followed to their underlying primitive.
//
// Generic type parameters and unknown named types fall back to PrimAny
// so the check doesn't false-positive while semantic resolution catches
// up.

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/prims"
)

// checkFieldTypeCompat walks every type / error body and checks each
// field's decorators against the resolved primitive category. Mixin
// members are skipped - they have no decorators of their own. Errors
// follow the same field shape as types.
func (a *analyzer) checkFieldTypeCompat() {
	for _, td := range a.pkg.Types {
		a.checkBodyTypeCompat(td.Name, td.Body)
	}
	for _, ed := range a.pkg.Errors {
		a.checkBodyTypeCompat(ed.Name, ed.Body)
	}
	for _, sd := range a.pkg.Scalars {
		a.checkScalarTypeCompat(sd)
	}
}

// checkBodyTypeCompat applies the per-decorator AppliesTo check to
// every Field in a type / error body.
func (a *analyzer) checkBodyTypeCompat(parent string, members []ast.TypeMember) {
	for _, m := range members {
		f, ok := m.(*ast.Field)
		if !ok {
			continue
		}
		actual := a.fieldPrimOf(f)
		for _, d := range f.Decorators {
			if d == nil {
				continue
			}
			if a.formatRawMismatch(d, actual, parent+"."+f.Name, describeTypeRef(f.Type)) {
				continue
			}
			spec, ok := Lookup(d.Name)
			if !ok || spec.AppliesTo == 0 {
				continue
			}
			if actual == 0 {
				// Unresolved field type - skip to avoid false positives
				// (e.g. generic parameter, qualified ref).
				continue
			}
			if spec.AppliesTo&actual == 0 {
				a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError,
					CodeDecoratorTypeMismatch,
					"@%s applies to %s fields, but %s.%s is %s",
					d.Name, spec.AppliesTo, parent, f.Name, actual)
			}
		}
	}
}

// checkScalarTypeCompat applies the same check to scalar declarations
// (`scalar Email string @format(email)`). The scalar's primitive is
// known directly from the AST. Also validates that the primitive
// slot holds an actual built-in (a typo like `scalar Check Check`
// would otherwise silently produce a no-op alias that breaks
// validator inheritance + codegen).
func (a *analyzer) checkScalarTypeCompat(sd *ast.ScalarDecl) {
	actual := PrimFromName(sd.Primitive)
	if sd.Primitive == "bytes" && HasRawFormat(sd.Decorators) {
		actual = PrimRawBytes
	}
	if actual == 0 || actual == PrimFile {
		// Not a recognised scalar primitive. `file` resolves to PrimFile
		// (non-zero) but is a multipart-upload wire keyword, not a Go type -
		// `scalar X file` would emit non-compiling `type X file`, so reject it
		// like an unknown primitive (mirroring the `any` rejection). Flag
		// explicitly so the user sees it at design time rather than via a
		// mysterious compile error in the generated Go.
		a.diag(sd.Pos, sd.Pos, lexer.SeverityError, CodeScalarBadPrimitive,
			"scalar %q primitive must be a built-in (got %q; expected one of string, bool, bytes, int, int8..int64, uint, uint8..uint64, float32, float64)",
			sd.Name, sd.Primitive)
		return
	}
	for _, d := range sd.Decorators {
		if d == nil {
			continue
		}
		if a.formatRawMismatch(d, actual, "scalar "+sd.Name, sd.Primitive) {
			continue
		}
		spec, ok := Lookup(d.Name)
		if !ok || spec.AppliesTo == 0 {
			continue
		}
		if spec.AppliesTo&actual == 0 {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError,
				CodeDecoratorTypeMismatch,
				"@%s applies to %s, but scalar %s is %s",
				d.Name, spec.AppliesTo, sd.Name, actual)
		}
	}
}

// formatRawMismatch reports `@format(raw)` sitting on anything but the
// raw-bytes shape, and returns true when it did so the caller skips the
// generic check for that decorator (one mistake, one diagnostic).
//
// `raw` is the one `@format` value the AppliesTo table cannot judge: it
// is an ARGUMENT, and `@format` legitimately applies to every
// string-shaped field - so `payload string @format(raw)` passes that
// check and has to be refused where the argument is read. A field that
// IS bytes resolves to [PrimRawBytes] instead, which is what makes every
// OTHER validator refuse on it through the generic path.
//
// subject is the diagnostic's subject ("Req.payload", "scalar Blob") and
// actualDesc how the refused type is spelt.
func (a *analyzer) formatRawMismatch(d *ast.Decorator, actual Prims, subject, actualDesc string) bool {
	if !isFormatRaw(d) || actual == PrimRawBytes {
		return false
	}
	a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
		"@format(raw) applies to bytes, but %s is %s - `raw` says the bytes already are the value in the message's own encoding, which only `bytes` carries",
		subject, actualDesc)
	return true
}

// fieldPrimOf is [analyzer.fieldPrim] for a whole FIELD: the raw-bytes
// shape is `bytes` plus the `@format(raw)` written on the field or on the
// scalar it names, so the type ref alone cannot classify it. The resolved
// IR already answers that question for codegen and the LSP, so it answers
// it here too rather than the analyser deciding a second way. Every other
// validator's AppliesTo misses [PrimRawBytes], which is what refuses them
// on a raw field through the ordinary compatibility check.
func (a *analyzer) fieldPrimOf(f *ast.Field) Prims {
	if f == nil {
		return 0
	}
	if ResolveField(f, a.pkg, a.proj).Category == CatRawBytes {
		return PrimRawBytes
	}
	return a.fieldPrim(f.Type)
}

// fieldPrim resolves a field's [ast.TypeRef] to a single primitive
// category. Returns 0 (PrimAny) for unresolved types so callers can skip
// the check rather than emit a misleading mismatch.
//
// Resolution rules:
//   - Array (`T[]`) and map (`map<K,V>`) collapse to PrimArray.
//   - Built-in primitives map directly via [PrimFromName].
//   - A scalar - bare or qualified `pkg.Name` - is followed to its
//     underlying primitive.
//   - Generic params and unknown names return 0.
func (a *analyzer) fieldPrim(t *ast.TypeRef) Prims {
	if t == nil {
		return 0
	}
	if t.Array || t.Map != nil {
		return PrimArray
	}
	if t.Named == nil || t.Named.Name == nil {
		return 0
	}
	if len(t.Named.Name.Parts) == 1 {
		if p := PrimFromName(t.Named.Name.Parts[0]); p != 0 {
			return p
		}
	}
	if sd := a.lookupScalar(t.Named); sd != nil {
		return PrimFromName(sd.Primitive)
	}
	return 0
}

// PrimFromName maps a built-in primitive name to its [Prims] category.
// Returns 0 for names this layer can't classify (custom types, `any`,
// `object` - those are handled by the caller). Exported so the LSP reuses the
// one classification instead of keeping its own copy.
func PrimFromName(name string) Prims {
	sp, ok := prims.Lookup(name)
	if !ok {
		return 0
	}
	switch sp.Kind {
	case prims.String, prims.Bytes:
		return PrimString
	case prims.Int, prims.Uint, prims.Float:
		return PrimNumber
	case prims.Bool:
		return PrimBool
	case prims.File:
		return PrimFile
	case prims.DateTime:
		return PrimDateTime
	}
	return 0
}
