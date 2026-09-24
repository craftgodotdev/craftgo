package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/prims"
)

// checkFieldTypeCompat checks the decorators of every type and error field,
// and of every scalar, against the primitive category they decorate.
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

// checkBodyTypeCompat checks each field decorator's AppliesTo in a type or
// error body.
func (a *analyzer) checkBodyTypeCompat(parent string, members []ast.TypeMember) {
	for _, f := range ast.Fields(members) {
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
				continue // not a primitive or scalar field
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

// checkScalarTypeCompat checks that a scalar wraps a built-in primitive,
// then checks its decorators against that primitive.
func (a *analyzer) checkScalarTypeCompat(sd *ast.ScalarDecl) {
	actual := PrimFromName(sd.Primitive)
	if sd.Primitive == "bytes" && HasRawFormat(sd.Decorators) {
		actual = PrimRawBytes
	}
	if actual == 0 || actual == PrimFile {
		// `file` is an upload keyword, not a type a scalar can wrap.
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

// formatRawMismatch reports `@format(raw)` on anything but raw bytes, which
// AppliesTo cannot catch because `raw` is an argument, and returns whether
// it did. subject names the decorated site and actualDesc its type.
func (a *analyzer) formatRawMismatch(d *ast.Decorator, actual Prims, subject, actualDesc string) bool {
	if !isFormatRaw(d) || actual == PrimRawBytes {
		return false
	}
	a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
		"@format(raw) applies to bytes, but %s is %s - `raw` says the bytes already are the value in the message's own encoding, which only `bytes` carries",
		subject, actualDesc)
	return true
}

// fieldPrimOf is [analyzer.fieldPrim] for a whole field: a bytes field is
// [PrimRawBytes] when `@format(raw)` sits on it or on its scalar.
func (a *analyzer) fieldPrimOf(f *ast.Field) Prims {
	if f == nil {
		return 0
	}
	if ResolveField(f, a.pkg, a.proj).Category == CatRawBytes {
		return PrimRawBytes
	}
	return a.fieldPrim(f.Type)
}

// fieldPrim returns t's primitive category: [PrimArray] for an array or
// map, the category of a built-in or of a scalar's primitive, else 0.
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

// PrimFromName returns the [Prims] category of a built-in primitive name;
// 0 for any other name, `any` and `object` included.
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
