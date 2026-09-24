package golang

import (
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

func collectDefaults(m *ast.Method, pkg *semantic.Package, pkgAlias string, r *projectResolver) []defaultBinding {
	if m.Request == nil {
		return nil
	}
	td, prefix := semantic.LookupMethodType(m.Request, pkg, r.Resolver)
	if td == nil {
		return nil
	}
	var out []defaultBinding
	for _, ff := range flattenFieldsWithNames(td, prefix, pkg, r, map[string]bool{}) {
		f := ff.Field
		if f.Type == nil || f.Type.Map != nil {
			continue
		}
		lit := defaultLiteral(f, pkg, r, pkgAlias)
		if lit == "" {
			continue
		}
		out = append(out, defaultBinding{
			GoName:  ff.Name,
			Literal: lit,
			Ptr:     goFieldIsPointer(f, pkg, r),
		})
	}
	return out
}

// defaultLiteral renders f's @default as Go source, or "" when it is absent or unrenderable.
func defaultLiteral(f *ast.Field, pkg *semantic.Package, r *projectResolver, pkgAlias string) string {
	for _, d := range f.Decorators {
		if d.Name != "default" || len(d.Args) != 1 {
			continue
		}
		return renderDefault(f.Type, d.Args[0].Value, pkg, r, pkgAlias)
	}
	return ""
}

// renderDefault renders v as a Go value of type t, qualifying a local enum or scalar with
// pkgAlias, or returns "" when it cannot.
func renderDefault(t *ast.TypeRef, v ast.Expr, pkg *semantic.Package, r *projectResolver, pkgAlias string) string {
	if t == nil {
		return ""
	}
	if t.Array {
		arr, ok := v.(*ast.ArrayLit)
		if !ok {
			return ""
		}
		elemT := t.ElemTypeRef()
		elemGo := qualifyNamed(goTypeRef(elemT), elemT, pkg, pkgAlias)
		if elemGo == "" {
			return ""
		}
		parts := make([]string, 0, len(arr.Elements))
		for _, e := range arr.Elements {
			p := renderDefault(elemT, e, pkg, r, pkgAlias)
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
		return enumDefaultConst(t, pkg, r, lit, pkgAlias)
	default:
		return ""
	}
	// The cast gives the pointer pre-fill's temp (`__d := PageSize(20)`) the field's type.
	if name := scalarDefaultGoName(t, pkg, r, pkgAlias); name != "" {
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
func scalarDefaultGoName(t *ast.TypeRef, pkg *semantic.Package, r *projectResolver, pkgAlias string) string {
	if t == nil || t.Array || t.Map != nil || t.Named == nil || t.Named.Name == nil {
		return ""
	}
	name := t.Named.Name.String()
	if r.LookupScalar(name) == nil {
		return ""
	}
	base := *t
	base.Optional = false
	return qualifyNamed(goTypeRef(&base), &base, pkg, pkgAlias)
}

// qualifyNamed prefixes goName with pkgAlias when t is a local enum or scalar; primitives and
// qualified references pass through.
func qualifyNamed(goName string, t *ast.TypeRef, pkg *semantic.Package, pkgAlias string) string {
	if goName == "" {
		return goName
	}
	if t == nil || t.Named == nil || t.Named.Name == nil {
		return goName
	}
	parts := t.Named.Name.Parts
	if len(parts) == 2 {
		return goName
	}
	if pkgAlias == "" || len(parts) != 1 {
		return goName
	}
	name := parts[0]
	if _, ok := pkg.Enums[name]; ok {
		return pkgAlias + "." + goName
	}
	if _, ok := pkg.Scalars[name]; ok {
		return pkgAlias + "." + goName
	}
	return goName
}

// enumDefaultConst resolves `@default(Member)` to the member's Go constant, qualified for the handler.
func enumDefaultConst(t *ast.TypeRef, pkg *semantic.Package, r *projectResolver, v *ast.IdentExpr, pkgAlias string) string {
	if t == nil || t.Named == nil || t.Named.Name == nil {
		return ""
	}
	if v.Name == nil || len(v.Name.Parts) != 1 {
		return ""
	}
	parts := t.Named.Name.Parts
	var ed *ast.EnumDecl
	var qualifier string
	switch len(parts) {
	case 1:
		ed = r.LookupEnum(parts[0])
		qualifier = pkgAlias
	case 2:
		ed = r.LookupEnum(t.Named.Name.String())
		// A cross-package enum's constants live in its own package.
		qualifier = parts[0]
	default:
		return ""
	}
	if ed == nil {
		return ""
	}
	valueName := v.Name.Parts[0]
	for _, m := range enumMembers(ed) {
		if m.DSLName == valueName {
			if qualifier != "" {
				return qualifier + "." + m.ConstName
			}
			return m.ConstName
		}
	}
	return ""
}
