package golang

import (
	"fmt"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// crossFieldChecks renders td's @requiresOneOf (at least one member set) and
// @mutuallyExclusive (at most one member set) checks. A group is td's own
// rule, so its message has no subject.
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
				out = append(out, requiresOneOfCheck(groupMembers(td, names, ctx), ctx))
			}
		case "mutuallyExclusive":
			names := semantic.CrossFieldNames(d)
			if len(names) >= 2 {
				out = append(out, mutuallyExclusiveCheck(groupMembers(td, names, ctx), ctx))
			}
		}
	}
	return out
}

// requiresOneOfCheck fails when every member is absent.
func requiresOneOfCheck(members []semantic.FlatField, ctx emitCtx) string {
	cond := strings.Join(memberPresence(members, false, ctx), " && ")
	return failIf(cond, "", fmt.Sprintf("requiresOneOf %v - at least one must be set", memberSubjects(members, ctx)), ctx)
}

// mutuallyExclusiveCheck counts the members set and fails above one, inside
// its own block so each check's `n` stays local.
func mutuallyExclusiveCheck(members []semantic.FlatField, ctx emitCtx) string {
	set := memberPresence(members, true, ctx)
	counters := make([]string, len(set))
	for i, p := range set {
		counters[i] = fmt.Sprintf("if %s {\nn++\n}", p)
	}
	fail := failIf("n > 1", "", fmt.Sprintf("mutuallyExclusive %v - at most one may be set", memberSubjects(members, ctx)), ctx)
	return fmt.Sprintf("{\nn := 0\n%s\n%s\n}", strings.Join(counters, "\n"), fail)
}

// groupMembers returns the members of td a cross-field group names, in the
// group's order, mixin-promoted members included.
func groupMembers(td *ast.TypeDecl, names []string, ctx emitCtx) []semantic.FlatField {
	byName := map[string]semantic.FlatField{}
	for _, ff := range semantic.FlattenFields(td, "", ctx.resolver.Resolver, resolvedGoFieldNames) {
		byName[ff.Field.Name] = ff
	}
	out := make([]semantic.FlatField, len(names))
	for i, name := range names {
		out[i] = byName[name]
	}
	return out
}

// memberPresence renders, for each member, the condition that it is set, or
// absent when !set, as the group's OpenAPI reads it: present and not null.
// semantic makes every member a Go value that is nil when absent; a raw one
// also holds an explicit null, as the four bytes `null`.
func memberPresence(members []semantic.FlatField, set bool, ctx emitCtx) []string {
	proj := ctx.resolver.Project()
	out := make([]string, len(members))
	for i, ff := range members {
		v := "v." + ff.Name
		raw := semantic.ResolveField(ff.Field, proj.Packages[ff.Home], proj).Category == semantic.CatRawBytes
		switch {
		case raw && set:
			out[i] = v + ` != nil && string(` + v + `) != "null"`
		case raw:
			out[i] = "(" + v + ` == nil || string(` + v + `) == "null")`
		case set:
			out[i] = v + " != nil"
		default:
			out[i] = v + " == nil"
		}
	}
	return out
}

// memberSubjects returns the name each member's own validation messages carry.
func memberSubjects(members []semantic.FlatField, ctx emitCtx) []string {
	out := make([]string, len(members))
	for i, ff := range members {
		out[i] = ctx.subject(ff.Field)
	}
	return out
}
