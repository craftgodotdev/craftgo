// Cross-field validators: @requiresOneOf / @mutuallyExclusive emission and
// the field presence / absence expressions they build on.
package codegen

import (
	"fmt"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// crossFieldChecks emits the type-level validators @requiresOneOf and
// @mutuallyExclusive. Each takes an array of field names; the
// generated code computes each field's "presence" via [presenceExpr]
// and then asserts the count constraint.
//
//	@requiresOneOf(["a", "b"])     → at least one must be present
//	@mutuallyExclusive(["a", "b"]) → at most one may be present
func crossFieldChecks(td *ast.TypeDecl, pkg *semantic.Package, r *ProjectResolver, uses map[string]bool) []string {
	if len(td.Decorators) == 0 {
		return nil
	}
	var out []string
	for _, d := range td.Decorators {
		switch d.Name {
		case "requiresOneOf":
			names := dedupeStrings(stringArrayDecoratorArg(d))
			if len(names) > 0 {
				out = append(out, requiresOneOfCheck(td, names, pkg, r, uses))
			}
		case "mutuallyExclusive":
			names := dedupeStrings(stringArrayDecoratorArg(d))
			if len(names) >= 2 {
				out = append(out, mutuallyExclusiveCheck(td, names, pkg, r, uses))
			}
		}
	}
	return out
}

// dedupeStrings drops repeat entries from a name list while preserving
// first-seen order. Used by cross-field codegen so a typo'd duplicate
// (`@requiresOneOf(a, a, b)`) doesn't produce `v.A == nil && v.A == nil`
// which `go vet` flags as a redundant boolean.
func dedupeStrings(in []string) []string {
	if len(in) <= 1 {
		return in
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// stringArrayDecoratorArg returns the field-name list passed to a
// type-level decorator like `@requiresOneOf` / `@mutuallyExclusive`.
// Three argument shapes are accepted, matching the syntax the
// semantic argument-shape validator allows:
//
//   - Variadic bare idents:    @requiresOneOf(email, phone)
//   - Variadic string literals: @requiresOneOf("email", "phone")
//   - Array shortcut:           @requiresOneOf(["email", "phone"])
//
// Returns nil when the decorator has no arguments at all.
func stringArrayDecoratorArg(d *ast.Decorator) []string {
	if len(d.Args) == 0 {
		return nil
	}
	// Array shortcut: single positional that's an [ ... ] literal.
	if arr, ok := d.Args[0].Value.(*ast.ArrayLit); ok && len(d.Args) == 1 {
		return collectStringOrIdent(arr.Elements)
	}
	// Variadic positional: each arg is its own ident or string lit.
	out := make([]string, 0, len(d.Args))
	for _, ag := range d.Args {
		if ag.Named || ag.Object != nil || ag.Nested != nil {
			continue
		}
		switch v := ag.Value.(type) {
		case *ast.StringLit:
			out = append(out, v.Value)
		case *ast.IdentExpr:
			if v.Name != nil {
				out = append(out, v.Name.String())
			}
		}
	}
	return out
}

// collectStringOrIdent extracts every string-lit / ident-expr value
// from an [ast.ArrayLit] elements slice, skipping anything else
// silently. Other shapes are caught upstream by the
// argument-shape validator.
func collectStringOrIdent(elems []ast.Expr) []string {
	out := make([]string, 0, len(elems))
	for _, e := range elems {
		switch v := e.(type) {
		case *ast.StringLit:
			out = append(out, v.Value)
		case *ast.IdentExpr:
			if v.Name != nil {
				out = append(out, v.Name.String())
			}
		}
	}
	return out
}

// requiresOneOfCheck emits a De Morgan'd absence-conjunction:
// "all fields are absent" → reject. The natural negation
// `!(presentA || presentB)` triggers `staticcheck`'s QF1001
// (De Morgan), so we invert each presence expression up-front and
// join with `&&` - the generated source is what `staticcheck` would
// rewrite to anyway.
func requiresOneOfCheck(td *ast.TypeDecl, names []string, pkg *semantic.Package, r *ProjectResolver, uses map[string]bool) string {
	uses["fmt"] = true
	parts := absenceParts(td, names, pkg, r)
	cond := strings.Join(parts, " && ")
	msg := fmt.Sprintf(`"%s: requiresOneOf %v - at least one must be set"`, td.Name, names)
	return ifReturnf(cond, msg)
}

// mutuallyExclusiveCheck emits a counter-based block: count how many
// of the listed fields are present and reject when > 1. The whole
// thing is wrapped in a bare `{ ... }` block so the `n` counter
// scopes locally - multiple @mutuallyExclusive declarations on the
// same struct don't shadow each other.
func mutuallyExclusiveCheck(td *ast.TypeDecl, names []string, pkg *semantic.Package, r *ProjectResolver, uses map[string]bool) string {
	uses["fmt"] = true
	parts := presenceParts(td, names, pkg, r)
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

// presenceParts returns one Go boolean expression per name in the
// list. Unknown names (typoed by the user) become a literal `false`
// so the generated code compiles even when the decorator references a
// missing field - the resulting check is a no-op for that slot.
func presenceParts(td *ast.TypeDecl, names []string, pkg *semantic.Package, r *ProjectResolver) []string {
	parts := make([]string, 0, len(names))
	for _, name := range names {
		f, goName := lookupField(td, name, pkg, r)
		if f == nil {
			parts = append(parts, unresolvedCrossFieldExpr(name))
			continue
		}
		parts = append(parts, presenceExpr(f, goName, pkg, r))
	}
	return parts
}

// unresolvedCrossFieldExpr is emitted when a cross-field group member
// can't be resolved to a real field. Semantic analysis rejects such
// references before codegen runs - per-package for local types,
// project-level ([refResolver.checkProjectFieldGroups]) for types
// promoting cross-package mixin fields - so reaching here means a
// semantic↔codegen drift. Emit an undefined identifier rather than a
// literal `false`: a `false` slot silently produces a no-op validator
// that hides the drift, whereas this fails `go build` with the
// offending member named.
func unresolvedCrossFieldExpr(name string) string {
	return "craftgoUnresolvedCrossFieldMember_" + GoFieldName(name)
}

// lookupField finds the Field a TypeDecl contributes by DSL field name,
// expanding embedded mixins so a cross-field decorator can reference a
// promoted field (`@requiresOneOf` over a field the type inherits). It also
// returns the field's dedup-resolved Go identifier so the cross-field access
// (`v.UserID_2`) matches the struct rather than colliding on the bare name.
// The Go access resolves through field promotion for a mixin-inherited field.
func lookupField(td *ast.TypeDecl, name string, pkg *semantic.Package, r *ProjectResolver) (*ast.Field, string) {
	for _, ff := range flattenFieldsWithNames(td, "", pkg, r, map[string]bool{}) {
		if ff.Field.Name == name {
			return ff.Field, ff.GoName
		}
	}
	return nil, ""
}

// presenceExpr returns the Go expression that's true when the field
// has a meaningful value (matching's definition):
//
//   - optional `T?` OR `@nullable T` (pointer) → `v.X != nil`
//   - slice / map           → `len(v.X) > 0`
//   - string                → `v.X != ""`
//   - numeric               → `v.X != 0`
//   - other                 → fall back to "true" (always present)
//
// `@nullable` forces the field to a Go pointer even on plain `T`. The
// pointer check must come BEFORE the value-shape branches so cross-
// field rules emit a nil-check rather than `v.X == ""` against a
// `*string` (which fails to compile).
func presenceExpr(f *ast.Field, goName string, pkg *semantic.Package, r *ProjectResolver) string {
	access := "v." + goName
	if f.Type == nil {
		return "true"
	}
	if goFieldIsPointer(f, pkg, r) {
		return access + " != nil"
	}
	if f.Type.Array || f.Type.Map != nil {
		return "len(" + access + ") > 0"
	}
	if f.Type.Named != nil {
		switch f.Type.Named.Name.String() {
		case "string":
			return access + ` != ""`
		case "int", "int8", "int16", "int32", "int64",
			"uint", "uint8", "uint16", "uint32", "uint64",
			"float32", "float64":
			return access + " != 0"
		case "bool":
			return access
		}
	}
	return "true"
}

// absenceParts is the De Morgan inverse of [presenceParts]: each entry
// is the Go expression that's true when the field is "missing". Used
// by [requiresOneOfCheck] so the emitted condition reads as
// `!a && !b && !c` (idiomatic) instead of `!(a || b || c)` (which
// staticcheck flags as QF1001).
func absenceParts(td *ast.TypeDecl, names []string, pkg *semantic.Package, r *ProjectResolver) []string {
	parts := make([]string, 0, len(names))
	for _, name := range names {
		f, goName := lookupField(td, name, pkg, r)
		if f == nil {
			// Unresolved member - semantic analysis rejects this before
			// codegen, so this is a drift guard, not a user path. Emit a
			// loud build failure (see [unresolvedCrossFieldExpr]) rather
			// than a silent literal that no-ops the validator.
			parts = append(parts, unresolvedCrossFieldExpr(name))
			continue
		}
		parts = append(parts, absenceExpr(f, goName, pkg, r))
	}
	return parts
}

// absenceExpr is the inverse of [presenceExpr]. Operators are flipped
// directly (`!=` ↔ `==`, `> 0` → `== 0`, `bool` → `!bool`) so the
// generated source is the form `staticcheck` recommends and no extra
// `!(...)` wrapping leaks into the output. Pointer-shape (`T?` or
// `@nullable T`) is checked first via [goFieldIsPointer] so the emit
// stays type-safe.
func absenceExpr(f *ast.Field, goName string, pkg *semantic.Package, r *ProjectResolver) string {
	access := "v." + goName
	if f.Type == nil {
		return "false"
	}
	if goFieldIsPointer(f, pkg, r) {
		return access + " == nil"
	}
	if f.Type.Array || f.Type.Map != nil {
		return "len(" + access + ") == 0"
	}
	if f.Type.Named != nil {
		switch f.Type.Named.Name.String() {
		case "string":
			return access + ` == ""`
		case "int", "int8", "int16", "int32", "int64",
			"uint", "uint8", "uint16", "uint32", "uint64",
			"float32", "float64":
			return access + " == 0"
		case "bool":
			return "!" + access
		}
	}
	return "false"
}
