package semantic

import (
	"strings"

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
		actual := ResolveField(f, a.pkg, a.proj).Prims()
		for _, d := range f.Decorators {
			if a.formatArgMismatch(d, actual, parent+"."+f.Name, f.Type.String()) {
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
	if sd.Primitive == "" {
		return // the parser reported the missing primitive
	}
	if !ScalarWraps(sd.Primitive) {
		a.diag(sd.Pos, sd.Pos, lexer.SeverityError, CodeScalarBadPrimitive,
			"scalar %q primitive must be a built-in (got %q; expected one of %s)",
			sd.Name, sd.Primitive, strings.Join(ScalarPrimitives(), ", "))
		return
	}
	actual := ScalarPrims(sd)
	for _, d := range sd.Decorators {
		if a.formatArgMismatch(d, actual, "scalar "+sd.Name, sd.Primitive) {
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

// formatArgMismatch reports a `@format` whose argument does not fit the
// decorated type - `raw` on anything but bytes, any other format on bytes -
// which AppliesTo cannot catch, and returns whether it did. subject names
// the decorated site and actualDesc its type.
func (a *analyzer) formatArgMismatch(d *ast.Decorator, actual Prims, subject, actualDesc string) bool {
	if d.Name != "format" || len(d.Args) == 0 {
		return false
	}
	name, ok := ast.TextValue(d.Args[0].Value)
	switch {
	case !ok:
		return false
	case name == FormatRaw && actual != PrimRawBytes:
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
			"@format(raw) applies to bytes, but %s is %s - `raw` says the bytes already are the value in the message's own encoding, which only `bytes` carries",
			subject, actualDesc)
	case name != FormatRaw && actual&(PrimBytes|PrimRawBytes) != 0:
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
			"@format(%s) applies to string, but %s is %s - a binary value has no text format; `raw` is the only format bytes take",
			name, subject, actualDesc)
	default:
		return false
	}
	return true
}

// PrimFromName returns the [Prims] category of a built-in primitive name;
// 0 for any other name, `any` and `object` included.
func PrimFromName(name string) Prims {
	sp, ok := prims.Lookup(name)
	if !ok {
		return 0
	}
	switch sp.Kind {
	case prims.String:
		return PrimString
	case prims.Bytes:
		return PrimBytes
	case prims.Int, prims.Uint:
		return PrimInteger
	case prims.Float:
		return PrimFloat
	case prims.Bool:
		return PrimBool
	case prims.File:
		return PrimFile
	case prims.DateTime:
		return PrimDateTime
	}
	return 0
}

// ScalarWraps reports whether a scalar may wrap built-in name: any
// classified primitive but `file`, an upload rather than a value.
func ScalarWraps(name string) bool {
	p := PrimFromName(name)
	return p != 0 && p != PrimFile
}

// ScalarPrimitives returns the built-ins a scalar may wrap, in catalogue
// order.
func ScalarPrimitives() []string {
	var out []string
	for _, sp := range prims.All() {
		if ScalarWraps(sp.Name) {
			out = append(out, sp.Name)
		}
	}
	return out
}

// ScalarPrims returns the category of scalar sd's values: [PrimRawBytes]
// for bytes carrying `@format(raw)`, else its primitive's.
func ScalarPrims(sd *ast.ScalarDecl) Prims {
	p := PrimFromName(sd.Primitive)
	if p == PrimBytes && HasRawFormat(sd.Decorators) {
		return PrimRawBytes
	}
	return p
}
