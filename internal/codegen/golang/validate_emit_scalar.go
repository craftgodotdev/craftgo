package golang

import (
	"fmt"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// scalarFieldPrimitive returns the DSL primitive of a flat scalar-typed field,
// or "" for any other field.
func scalarFieldPrimitive(f *ast.Field, ctx emitCtx) string {
	if f == nil || f.Type == nil || f.Type.Array || f.Type.Map != nil || f.Type.Named == nil || f.Type.Named.Name == nil {
		return ""
	}
	sd := ctx.resolver.LookupScalar(f.Type.Named.Name.String())
	if sd == nil {
		return ""
	}
	return sd.Primitive
}

// enumFieldPrimitive returns "int" or "string" for a flat enum-typed field, or
// "" for any other field.
func enumFieldPrimitive(f *ast.Field, ctx emitCtx) string {
	if f == nil || f.Type == nil || f.Type.Array || f.Type.Map != nil || f.Type.Named == nil || f.Type.Named.Name == nil {
		return ""
	}
	ed := ctx.resolver.LookupEnum(f.Type.Named.Name.String())
	if ed == nil {
		return ""
	}
	if semantic.EnumKind(ed) == ast.EnumInt {
		return "int"
	}
	return "string"
}

// scalarFieldLevelChecks renders the constraints declared on a scalar- or
// enum-typed field against its value converted to primDSL in a local `_sv`.
func scalarFieldLevelChecks(f *ast.Field, access, primDSL string, ctx emitCtx) string {
	// sf has no decorators: a copied `@nullable` would guard and deref `_sv` again.
	sf := &ast.Field{
		Name: f.Name,
		Type: &ast.TypeRef{
			Named: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{primDSL}}},
		},
	}
	const local = "_sv"
	var checks []string
	for _, d := range f.Decorators {
		check := goChecks[d.Name]
		if check == nil {
			continue
		}
		if s := check(sf, local, d, ctx); s != "" {
			checks = append(checks, s)
		}
	}
	if len(checks) == 0 {
		return ""
	}
	body := strings.Join(checks, "\n")
	primGo := scalarPrimitiveGo(primDSL)
	switch {
	case goFieldIsPointer(f, ctx.pkg, ctx.resolver):
		return fmt.Sprintf("if %s != nil {\n%s := %s(*%s)\n%s\n}", access, local, primGo, access, body)
	case fieldNeedsNilGuard(f):
		// Not a pointer (a scalar over bytes), but nil is still the absent value.
		return fmt.Sprintf("if %s != nil {\n%s := %s(%s)\n%s\n}", access, local, primGo, access, body)
	default:
		return fmt.Sprintf("{\n%s := %s(%s)\n%s\n}", local, primGo, access, body)
	}
}

// scalarDeclHasValidators reports whether sd gets a Validate() method: it
// declares a constraint decorator and is not raw.
func scalarDeclHasValidators(sd *ast.ScalarDecl) bool {
	if sd == nil || semantic.HasRawFormat(sd.Decorators) {
		// A raw scalar aliases wire.Raw, which cannot take a method here.
		return false
	}
	for _, d := range sd.Decorators {
		if goChecks[d.Name] != nil {
			return true
		}
	}
	return false
}

// scalarValidateChecks renders the body of sd's Validate(), checking the value
// receiver converted to its primitive (`string(v)`).
func scalarValidateChecks(sd *ast.ScalarDecl, ctx emitCtx) []string {
	synth := &ast.Field{
		// No name: the using field wraps the error with its own.
		Name: "",
		Type: &ast.TypeRef{
			Named: &ast.NamedTypeRef{
				Name: &ast.QualifiedIdent{Parts: []string{sd.Primitive}},
			},
		},
	}
	access := scalarPrimitiveGo(sd.Primitive) + "(v)"
	var out []string
	for _, d := range sd.Decorators {
		check := goChecks[d.Name]
		if check == nil {
			continue
		}
		if s := check(synth, access, d, ctx); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// enumValidateChecks renders the body of ed's Validate(): a switch over its
// members' consts, or nothing for an enum without members. The error has no
// subject: the using field wraps it with its name.
func enumValidateChecks(ed *ast.EnumDecl) []string {
	members := enumMembers(ed)
	if len(members) == 0 {
		return nil
	}
	consts := make([]string, len(members))
	for i, m := range members {
		consts[i] = m.ConstName
	}
	return []string{fmt.Sprintf(`switch v {
case %s:
default:
return fmt.Errorf("invalid %s value")
}`, strings.Join(consts, ", "), ed.Name)}
}
