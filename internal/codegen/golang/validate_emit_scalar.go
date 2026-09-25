package golang

import (
	"fmt"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// primValueChecks renders the constraints declared on a scalar- or enum-typed
// field rf, held in t, against its value converted to its primitive in a local `_sv`.
func primValueChecks(rf semantic.ResolvedField, t checkTarget, ctx emitCtx) string {
	const local = "_sv"
	checks := decoratorChecks(primTarget(local, rf.ResolvedPrim, escapeErrorfName(rf.Field.Name)), rf.Field.Decorators, ctx)
	if len(checks) == 0 {
		return ""
	}
	body := strings.Join(checks, "\n")
	primGo := scalarPrimitiveGo(rf.ResolvedPrim)
	switch {
	case t.pointer:
		return fmt.Sprintf("if %s != nil {\n%s := %s(*%s)\n%s\n}", t.access, local, primGo, t.access, body)
	case t.nilGuard:
		// Not a pointer (a scalar over bytes), but nil is still the absent value.
		return fmt.Sprintf("if %s != nil {\n%s := %s(%s)\n%s\n}", t.access, local, primGo, t.access, body)
	default:
		return fmt.Sprintf("{\n%s := %s(%s)\n%s\n}", local, primGo, t.access, body)
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
// receiver converted to its primitive (`string(v)`). The errors have no
// subject: the using field wraps them with its own.
func scalarValidateChecks(sd *ast.ScalarDecl, ctx emitCtx) []string {
	return decoratorChecks(primTarget(scalarPrimitiveGo(sd.Primitive)+"(v)", sd.Primitive, ""), sd.Decorators, ctx)
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
