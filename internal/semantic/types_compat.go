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
//   1. Inspecting the AST [TypeRef] modifiers: `T[]` and `map<K,V>`
//      collapse to PrimArray.
//   2. Looking up the named type - built-in primitives map directly;
//      custom scalars are followed via [Package.Scalars] to their
//      underlying primitive.
//
// Generic type parameters and unknown named types fall back to PrimAny
// so the check doesn't false-positive while semantic resolution catches
// up.

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
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
		actual := a.fieldPrim(f.Type)
		for _, d := range f.Decorators {
			if d == nil {
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
// known directly from the AST.
func (a *analyzer) checkScalarTypeCompat(sd *ast.ScalarDecl) {
	actual := primFromName(sd.Primitive)
	if actual == 0 {
		return
	}
	for _, d := range sd.Decorators {
		if d == nil {
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

// fieldPrim resolves a field's [TypeRef] to a single primitive
// category. Returns 0 (PrimAny) for unresolved / cross-package types
// so callers can skip the check rather than emit a misleading mismatch.
//
// Resolution rules:
//   - Array (`T[]`) and map (`map<K,V>`) collapse to PrimArray.
//   - Built-in primitives map directly via [primFromName].
//   - Named refs that match a scalar in pkg.Scalars are followed; the
//     scalar's underlying primitive wins.
//   - Cross-package qualified names (`pkg.Type`) and generic params
//     return 0 - the qualified-ref pass already flagged them.
func (a *analyzer) fieldPrim(t *ast.TypeRef) Prims {
	if t == nil {
		return 0
	}
	if t.Array || t.Map != nil {
		return PrimArray
	}
	if t.Named == nil || t.Named.Name == nil || len(t.Named.Name.Parts) != 1 {
		return 0
	}
	name := t.Named.Name.Parts[0]
	if p := primFromName(name); p != 0 {
		return p
	}
	// Custom scalar: follow to its underlying primitive.
	if sd, ok := a.pkg.Scalars[name]; ok {
		return primFromName(sd.Primitive)
	}
	return 0
}

// primFromName maps a built-in primitive name to its [Prims] category.
// Returns 0 for names this layer can't classify (custom types, `any`,
// `object` - those are handled by the caller).
func primFromName(name string) Prims {
	switch name {
	case "string", "bytes":
		return PrimString
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64":
		return PrimNumber
	case "bool":
		return PrimBool
	case "file":
		return PrimFile
	}
	return 0
}
