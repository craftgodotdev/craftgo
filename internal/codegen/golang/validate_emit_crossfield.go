package golang

import (
	"fmt"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
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

// requiresOneOfCheck fails when every named member is nil.
func requiresOneOfCheck(td *ast.TypeDecl, names []string, ctx emitCtx) string {
	cond := strings.Join(memberNilExprs(td, names, "==", ctx), " && ")
	msg := fmt.Sprintf(`"%s: requiresOneOf %v - at least one must be set"`, td.Name, names)
	return ifReturnf(cond, msg, ctx)
}

// mutuallyExclusiveCheck counts the named members set and fails above one,
// inside its own block so each check's `n` stays local.
func mutuallyExclusiveCheck(td *ast.TypeDecl, names []string, ctx emitCtx) string {
	ctx.uses["fmt"] = true
	set := memberNilExprs(td, names, "!=", ctx)
	counters := make([]string, len(set))
	for i, p := range set {
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

// memberNilExprs renders `v.<Member> <op> nil` for each named member of td,
// mixin-promoted members included; semantic makes every member of a
// cross-field group a Go value that is nil exactly when absent.
func memberNilExprs(td *ast.TypeDecl, names []string, op string, ctx emitCtx) []string {
	goNames := map[string]string{}
	for _, ff := range semantic.FlattenFields(td, "", ctx.resolver.Resolver, resolvedGoFieldNames) {
		goNames[ff.Field.Name] = ff.Name
	}
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = "v." + goNames[name] + " " + op + " nil"
	}
	return out
}
