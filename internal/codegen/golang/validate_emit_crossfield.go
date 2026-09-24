package golang

import (
	"fmt"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// crossFieldChecks renders td's @requiresOneOf (at least one member set) and
// @mutuallyExclusive (at most one member set) checks.
func crossFieldChecks(td *ast.TypeDecl, ctx emitCtx) []string {
	if len(td.Decorators) == 0 {
		return nil
	}
	var out []string
	for _, d := range td.Decorators {
		switch d.Name {
		case "requiresOneOf":
			names := semantic.CrossFieldNames(d)
			if len(names) > 0 {
				out = append(out, requiresOneOfCheck(td, names, ctx))
			}
		case "mutuallyExclusive":
			names := semantic.CrossFieldNames(d)
			if len(names) >= 2 {
				out = append(out, mutuallyExclusiveCheck(td, names, ctx))
			}
		}
	}
	return out
}

// requiresOneOfCheck fails when every named member is absent.
func requiresOneOfCheck(td *ast.TypeDecl, names []string, ctx emitCtx) string {
	parts := absenceParts(td, names, ctx)
	cond := strings.Join(parts, " && ")
	msg := fmt.Sprintf(`"%s: requiresOneOf %v - at least one must be set"`, td.Name, names)
	return ifReturnf(cond, msg, ctx)
}

// mutuallyExclusiveCheck counts the named members present and fails above one,
// inside its own block so each check's `n` stays local.
func mutuallyExclusiveCheck(td *ast.TypeDecl, names []string, ctx emitCtx) string {
	ctx.uses["fmt"] = true
	parts := presenceParts(td, names, ctx)
	counters := make([]string, len(parts))
	for i, p := range parts {
		counters[i] = fmt.Sprintf("if %s {\nn++\n}", p)
	}
	return fmt.Sprintf(`{
n := 0
%s
if n > 1 {
return fmt.Errorf("%s: mutuallyExclusive %v - at most one may be set")
}
}`, strings.Join(counters, "\n"), td.Name, names)
}

// presenceParts returns the [presenceExpr] of each named member; a name no field
// matches renders [unresolvedCrossFieldExpr].
func presenceParts(td *ast.TypeDecl, names []string, ctx emitCtx) []string {
	parts := make([]string, 0, len(names))
	for _, name := range names {
		f, goName := lookupField(td, name, ctx)
		if f == nil {
			parts = append(parts, unresolvedCrossFieldExpr(name))
			continue
		}
		parts = append(parts, presenceExpr(f, goName, ctx))
	}
	return parts
}

// unresolvedCrossFieldExpr renders an unknown member as an undefined identifier,
// so the generated code fails to build naming it.
func unresolvedCrossFieldExpr(name string) string {
	return "craftgoUnresolvedCrossFieldMember_" + goFieldName(name)
}

// lookupField finds td's field by DSL name, mixin-promoted fields included, and
// returns it with its deduped Go name.
func lookupField(td *ast.TypeDecl, name string, ctx emitCtx) (*ast.Field, string) {
	for _, ff := range flattenFieldsWithNames(td, "", ctx.resolver) {
		if ff.Field.Name == name {
			return ff.Field, ff.Name
		}
	}
	return nil, ""
}

// presenceExpr returns the condition that the member holds a value: non-nil for
// a pointer or raw field (an explicit `null` is present), else non-empty or non-zero.
func presenceExpr(f *ast.Field, goName string, ctx emitCtx) string {
	access := "v." + goName
	if f.Type == nil {
		return "true"
	}
	if goFieldIsPointer(f, ctx.pkg, ctx.resolver) || isRawBytesField(f, ctx.pkg, ctx.resolver) {
		return access + " != nil"
	}
	if f.Type.Array || f.Type.Map != nil {
		return "len(" + access + ") > 0"
	}
	if f.Type.Named != nil {
		switch sp, _ := prims.Lookup(f.Type.Named.Name.String()); sp.Kind {
		case prims.String:
			return access + ` != ""`
		case prims.Int, prims.Uint, prims.Float:
			return access + " != 0"
		case prims.Bool:
			return access
		case prims.DateTime:
			return "!" + access + ".IsZero()"
		}
	}
	return "true"
}

// absenceParts is [presenceParts] negated member by member, so the condition
// needs no `!(...)` (staticcheck QF1001).
func absenceParts(td *ast.TypeDecl, names []string, ctx emitCtx) []string {
	parts := make([]string, 0, len(names))
	for _, name := range names {
		f, goName := lookupField(td, name, ctx)
		if f == nil {
			parts = append(parts, unresolvedCrossFieldExpr(name))
			continue
		}
		parts = append(parts, absenceExpr(f, goName, ctx))
	}
	return parts
}

// absenceExpr returns the negation of [presenceExpr], each operator flipped.
func absenceExpr(f *ast.Field, goName string, ctx emitCtx) string {
	access := "v." + goName
	if f.Type == nil {
		return "false"
	}
	if goFieldIsPointer(f, ctx.pkg, ctx.resolver) || isRawBytesField(f, ctx.pkg, ctx.resolver) {
		return access + " == nil"
	}
	if f.Type.Array || f.Type.Map != nil {
		return "len(" + access + ") == 0"
	}
	if f.Type.Named != nil {
		switch sp, _ := prims.Lookup(f.Type.Named.Name.String()); sp.Kind {
		case prims.String:
			return access + ` == ""`
		case prims.Int, prims.Uint, prims.Float:
			return access + " == 0"
		case prims.Bool:
			return "!" + access
		case prims.DateTime:
			return access + ".IsZero()"
		}
	}
	return "false"
}
