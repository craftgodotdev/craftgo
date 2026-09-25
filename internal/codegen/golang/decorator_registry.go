package golang

import (
	"fmt"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// emitCtx is the state the check emitters of one validate.go share: the
// imports it uses, its regex vars, and the package and project lookups.
type emitCtx struct {
	pkg      *semantic.Package
	uses     map[string]bool
	regexes  *regexRegistry
	resolver *projectResolver
}

// regexRegistry interns regex patterns as package-level vars (`_pattern0`,
// `_pattern1`, ...) compiled once at init.
type regexRegistry struct {
	byPattern map[string]string
	entries   []regexVar
}

// newRegexRegistry returns an empty registry.
func newRegexRegistry() *regexRegistry {
	return &regexRegistry{byPattern: map[string]string{}}
}

// intern returns the var name for pattern, allocating one on first use; "" for
// an empty pattern.
func (r *regexRegistry) intern(pattern string) string {
	if r == nil || pattern == "" {
		return ""
	}
	if name, ok := r.byPattern[pattern]; ok {
		return name
	}
	name := fmt.Sprintf("_pattern%d", len(r.entries))
	r.byPattern[pattern] = name
	r.entries = append(r.entries, regexVar{Name: name, Pattern: pattern})
	return name
}

// fieldChecks renders field rf's required check and the constraints declared
// on it, held in t; a scalar- or enum-typed field checks them via [primValueChecks].
func fieldChecks(rf semantic.ResolvedField, t checkTarget, ctx emitCtx) []string {
	var out []string
	// A @nullable field gets no required check: a missing key and `null` both decode to nil.
	if rf.RuntimeEnforced {
		if s := requiredCheck(rf, t, ctx); s != "" {
			out = append(out, s)
		}
	}
	if rf.Category == semantic.CatScalar || rf.Category == semantic.CatEnum {
		if blk := primValueChecks(rf, t, ctx); blk != "" {
			out = append(out, blk)
		}
		return out
	}
	return append(out, decoratorChecks(t, rf.Field.Decorators, ctx)...)
}

// decoratorChecks renders the check of each constraint decorator of decs on t.
func decoratorChecks(t checkTarget, decs []*ast.Decorator, ctx emitCtx) []string {
	var out []string
	for _, d := range decs {
		check := goChecks[d.Name]
		if check == nil {
			continue
		}
		if s := check(t, d, ctx); s != "" {
			out = append(out, s)
		}
	}
	return out
}
