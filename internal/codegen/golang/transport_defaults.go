package golang

import (
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/prims"
)

// collectDefaults pre-fills the request fields that carry a @default, before the request is bound.
func collectDefaults(fields []resolvedField, r *projectResolver, imports *importSet) []defaultBinding {
	var out []defaultBinding
	for _, rf := range fields {
		f, arg := rf.Field, firstArg(rf.Field.Decorators, "default")
		if !rf.HasDefValue || arg == nil || f.Type == nil || f.Type.Map != nil {
			continue
		}
		lit := renderDefault(f.Type, arg.Value, r, imports)
		if lit == "" {
			continue
		}
		out = append(out, defaultBinding{GoName: rf.GoName, Literal: lit, Ptr: rf.IsPointer})
	}
	return out
}

// renderDefault renders v as a Go value of type t, or returns "" when it cannot.
func renderDefault(t *ast.TypeRef, v ast.Expr, r *projectResolver, imports *importSet) string {
	if t == nil {
		return ""
	}
	if t.Array {
		arr, ok := v.(*ast.ArrayLit)
		if !ok {
			return ""
		}
		elemT := t.ElemTypeRef()
		elemGo := imports.goType(elemT)
		if elemGo == "" {
			return ""
		}
		parts := make([]string, 0, len(arr.Elements))
		for _, e := range arr.Elements {
			p := renderDefault(elemT, e, r, imports)
			if p == "" {
				return ""
			}
			parts = append(parts, p)
		}
		return "[]" + elemGo + "{" + strings.Join(parts, ", ") + "}"
	}
	var s string
	switch lit := v.(type) {
	case *ast.StringLit:
		s = strconv.Quote(lit.Value)
	case *ast.IntLit:
		s = strconv.FormatInt(lit.Value, 10)
	case *ast.FloatLit:
		s = formatFloatLit(lit.Value)
	case *ast.BoolLit:
		if lit.Value {
			s = "true"
		} else {
			s = "false"
		}
	case *ast.IdentExpr:
		// An enum constant is already typed.
		return enumDefaultConst(t, r, lit, imports)
	default:
		return ""
	}
	// The cast gives the pointer pre-fill's temp (`__d := PageSize(20)`) the field's type.
	if name := scalarDefaultGoName(t, r, imports); name != "" {
		return name + "(" + s + ")"
	}
	if cast := primitiveDefaultCast(t, v); cast != "" {
		return cast + "(" + s + ")"
	}
	return s
}

// formatFloatLit renders f as a Go float literal, appending ".0" to a whole number so the
// pointer pre-fill's temp is not inferred as int.
func formatFloatLit(f float64) string {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eEnN") {
		s += ".0"
	}
	return s
}

// primitiveDefaultCast returns the numeric primitive t that literal v must be cast to for the
// pointer pre-fill, or "" when the literal's own type (int, or float64 for a float) fits.
func primitiveDefaultCast(t *ast.TypeRef, v ast.Expr) string {
	if t == nil || t.Named == nil || t.Named.Name == nil || len(t.Named.Name.Parts) != 1 {
		return ""
	}
	prim := t.Named.Name.Parts[0]
	switch v.(type) {
	case *ast.IntLit:
		if prim == "int" {
			return ""
		}
	case *ast.FloatLit:
		if prim == "float64" {
			return ""
		}
	default:
		return ""
	}
	if prims.IsNumeric(prim) && prim != "int" {
		return prim
	}
	return ""
}

// scalarDefaultGoName returns the qualified value type of scalar t (`types.PageSize` for
// `PageSize?`), or "" when t is not a scalar.
func scalarDefaultGoName(t *ast.TypeRef, r *projectResolver, imports *importSet) string {
	if t == nil || t.Array || t.Map != nil || t.Named == nil || t.Named.Name == nil {
		return ""
	}
	if r.LookupScalar(t.Named.Name.String()) == nil {
		return ""
	}
	base := *t
	base.Optional = false
	return imports.goType(&base)
}

// enumDefaultConst resolves `@default(Member)` to the member's Go constant, qualified for the handler.
func enumDefaultConst(t *ast.TypeRef, r *projectResolver, v *ast.IdentExpr, imports *importSet) string {
	if t == nil || t.Named == nil || t.Named.Name == nil {
		return ""
	}
	if v.Name == nil || len(v.Name.Parts) != 1 {
		return ""
	}
	ed := r.LookupEnum(t.Named.Name.String())
	if ed == nil {
		return ""
	}
	valueName := v.Name.Parts[0]
	for _, m := range enumMembers(ed) {
		if m.DSLName == valueName {
			// A cross-package enum's constants live in its own package.
			if parts := t.Named.Name.Parts; len(parts) == 2 {
				return imports.qualify(parts[0] + "." + m.ConstName)
			}
			return imports.qualify(m.ConstName)
		}
	}
	return ""
}
