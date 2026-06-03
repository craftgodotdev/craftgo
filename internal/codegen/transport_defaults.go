// Transport: @default literal rendering + Go pre-fill code generation.
package codegen

import (
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

func collectDefaults(m *ast.Method, pkg *semantic.Package, pkgAlias string, r *ProjectResolver) []defaultBinding {
	if m.Request == nil {
		return nil
	}
	// Resolve via the shared helper so the qualified-request prefix (and the
	// resolver fallback) match the binder exactly — otherwise a defaulted
	// field promoted from a nested mixin of a qualified request is dropped
	// from the pre-fill while the binder binds it and OpenAPI advertises the
	// default, leaving the client with zero values.
	td, prefix := lookupMethodType(m.Request, pkg, r)
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
			GoName:  ff.GoName,
			Literal: lit,
			Ptr:     goFieldIsPointer(f, pkg, r),
		})
	}
	return out
}

// defaultLiteral returns the Go-source form of a `@default(...)`
// value, or "" when the decorator is absent or unrenderable. The
// supported shapes are: string / int / float / bool literals,
// IdentExpr (resolved to an enum constant), and ArrayLit (rendered
// recursively as a Go slice literal). Map / struct / generic field
// types fall through to "" - the semantic phase has already flagged
// the unsupported combination.
func defaultLiteral(f *ast.Field, pkg *semantic.Package, r *ProjectResolver, pkgAlias string) string {
	for _, d := range f.Decorators {
		if d.Name != "default" || len(d.Args) != 1 {
			continue
		}
		return renderDefault(f.Type, d.Args[0].Value, pkg, r, pkgAlias)
	}
	return ""
}

// renderDefault produces the Go source for one `@default(...)` value
// against the field's resolved type. Recurses through array / array
// elements; returns "" when the value can't be rendered (mixed kind,
// unknown enum, struct element, etc.). pkgAlias is the Go-side
// alias of the types package used by the request struct (e.g.
// "types") so named-type references (enums, scalars) emit as
// `<alias>.<Name>` and stay valid in the handler's own package.
func renderDefault(t *ast.TypeRef, v ast.Expr, pkg *semantic.Package, r *ProjectResolver, pkgAlias string) string {
	if t == nil {
		return ""
	}
	if t.Array {
		arr, ok := v.(*ast.ArrayLit)
		if !ok {
			return ""
		}
		elemT := t.ElemTypeRef()
		elemGo := qualifyNamed(GoTypeRef(elemT), elemT, pkg, r, pkgAlias)
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
		// Enum default: the const (`types.ColorRed`) is already typed,
		// so no cast is added.
		return enumDefaultConst(t, pkg, r, lit, pkgAlias)
	default:
		return ""
	}
	// Scalar field default: cast the primitive literal to the scalar's
	// defined Go type. Scalars emit as DEFINED types (`type PageSize
	// int`), not aliases, so without the cast the pointer-prefill
	// `__d := <lit>` infers the bare primitive and `&__d` (a `*int`)
	// fails to assign to the field's `*PageSize`. Casting
	// (`PageSize(20)`) makes `__d` the scalar type. Harmless for the
	// non-pointer case — `req.X = PageSize(20)` is identical to the
	// untyped-constant form.
	if name := scalarDefaultGoName(t, pkg, r, pkgAlias); name != "" {
		return name + "(" + s + ")"
	}
	// Narrow numeric primitive default: same pointer-prefill problem as a
	// scalar. `__d := <lit>` infers Go `int` for an int literal and
	// `float64` for a float one, so for an int32 / int64 / uint* /
	// float32 field `&__d` is the wrong pointer type. Cast the literal to
	// the field's primitive (`int32(1)`) so `__d` carries it.
	if cast := primitiveDefaultCast(t, v); cast != "" {
		return cast + "(" + s + ")"
	}
	return s
}

// formatFloatLit renders a float `@default` literal as Go source that is
// unambiguously a float. `strconv.FormatFloat` drops the fraction of a
// whole-number value (`1.0` → "1"), which Go would then infer as `int` — so
// the optional-field pointer pre-fill `__d := 1` becomes `*int`, not the
// field's `*float64`. Appending `.0` when the rendering carries no decimal
// point or exponent keeps it a float literal (`1.0`), so no cast is needed
// and a fractional value like `0.5` is unchanged.
func formatFloatLit(f float64) string {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eEnN") {
		s += ".0"
	}
	return s
}

// primitiveDefaultCast returns the Go primitive a numeric `@default`
// literal must be cast to so a `*T` pointer pre-fill assigns the right
// type, or "" when no cast is needed. An int literal is already Go `int`
// and a float literal `float64`; every other numeric width or sign
// (int8 / int16 / int32 / int64 / uint* / float32, and a float-typed
// field taking an int literal) needs the cast. string / bool literals
// and non-primitive types (scalars, enums) are handled elsewhere.
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
	switch prim {
	case "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64":
		return prim
	}
	return ""
}

// scalarDefaultGoName returns the qualified Go type name of a scalar
// reference (`types.PageSize`, `shared.CurrencyCode`) so a `@default`
// literal can be cast to it, or "" when t is not a flat scalar ref
// (array / map / primitive / enum / struct). Array elements clear
// Optional via [ast.TypeRef.ElemTypeRef] before reaching here; a top-level
// optional scalar (`PageSize?`) keeps Optional set, so the clone drops
// it — the cast targets the value type `PageSize`, not `*PageSize`.
func scalarDefaultGoName(t *ast.TypeRef, pkg *semantic.Package, r *ProjectResolver, pkgAlias string) string {
	if t == nil || t.Array || t.Map != nil || t.Named == nil || t.Named.Name == nil {
		return ""
	}
	name := t.Named.Name.String()
	if _, ok := pkg.Scalars[name]; !ok && r.LookupScalar(name) == nil {
		return ""
	}
	base := *t
	base.Optional = false
	return qualifyNamed(GoTypeRef(&base), &base, pkg, r, pkgAlias)
}

// qualifyNamed prefixes a Go type reference with `<pkgAlias>.` when
// the underlying TypeRef points at a LOCAL project-defined named type
// (enum or scalar) — those constants live in the request's types
// package and the handler file needs the alias to reach them.
// Primitives stay bare. CROSS-PACKAGE refs already carry their own
// qualifier (`xshared.XColor`) and pass through untouched; without
// this case the qualified Go name double-prefixes to
// `<reqAlias>.xshared.XColor` and the cast fails to compile.
func qualifyNamed(goName string, t *ast.TypeRef, pkg *semantic.Package, r *ProjectResolver, pkgAlias string) string {
	if goName == "" {
		return goName
	}
	if t == nil || t.Named == nil || t.Named.Name == nil {
		return goName
	}
	parts := t.Named.Name.Parts
	// Qualified ref `pkg.X`: goName is already `pkg.X`, leave as-is.
	if len(parts) == 2 {
		name := t.Named.Name.String()
		if r.LookupEnum(name) != nil || r.LookupScalar(name) != nil {
			return goName
		}
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

// enumDefaultConst resolves an `@default(<Ident>)` reference to its
// emitted Go constant name. The semantic phase has already validated
// that the field type is an enum and the ident matches a declared
// value; this function reproduces buildEnumView's dedup so the
// const-name lookup hits the same identifier even when value names
// differ only in case.
func enumDefaultConst(t *ast.TypeRef, pkg *semantic.Package, r *ProjectResolver, v *ast.IdentExpr, pkgAlias string) string {
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
		ed = pkg.Enums[parts[0]]
		qualifier = pkgAlias
	case 2:
		ed = r.LookupEnum(t.Named.Name.String())
		// Cross-pkg: the Go constants live in the foreign package, so
		// the qualifier is the foreign pkg alias (`xshared`), not the
		// caller's request-types alias.
		qualifier = parts[0]
	default:
		return ""
	}
	if ed == nil {
		return ""
	}
	valueName := v.Name.Parts[0]
	// Read the shared enumMembers resolver so this const name uses the SAME
	// dedup the enum declaration (buildEnumView) and the validate case-list
	// emit — they must reference the identical Go const.
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
