// Validate codegen lives across five files in this package, organised
// by layer rather than by decorator:
//
//   - validate.go          driver - orchestrates Generate / collect / template
//   - validate_registry.go decorator → emit-function dispatch table
//   - validate_emit.go     per-validator emitters + cross-cutting helpers
//   - validate_args.go     decorator-argument extractors (intArg, sizeArg, ...)
//   - validate_types.go    field-shape predicates (isStringOrOptString, ...)
//
// To add a new validator: write its emit function in validate_emit.go,
// register it as one row in `validators` (validate_registry.go). Type
// guards and arg helpers are reusable from validate_types.go /
// validate_args.go - most new validators won't need new ones.

package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/semantic"
)

// validateData is the template input for `validate.tmpl`. It is computed
// up front so the template stays declarative - every conditional is
// resolved in Go code where unit tests can pin behaviour.
type validateData struct {
	Package string
	Imports []string
	Types   []validatorType
}

// validatorType is one Validate() method block in `validate.tmpl`.
// TypeParams is non-empty for generic decls - the template uses it to
// build the receiver suffix `[T any, ...]` so the method is declared on
// the parametric type itself, e.g. `func (v *Page[T]) Validate() error`.
type validatorType struct {
	Name       string
	TypeParams []string
	Checks     []string
}

// GenerateValidators writes `validate.go` next to `types.go`. The file
// adds a `Validate() error` method to every concrete TypeDecl. Types
// without any constraints get an empty stub so handlers can call
// `req.Validate()` uniformly.
//
// Equivalent to [GenerateValidatorsPackage] with a nil [CrossPkg]
// context - kept for backward compatibility with single-package
// callers and tests.
func GenerateValidators(pkg *semantic.Package, outDir string) error {
	return GenerateValidatorsPackage(pkg, outDir, nil)
}

// GenerateValidatorsPackage is the multi-package variant of
// [GenerateValidators]. crossPkg adds Go imports for every cross-
// package alias used in pkg's field types so `req.User.Validate()`
// can dispatch to the sibling package's validator.
//
// Equivalent to [GenerateValidatorsWith] with a nil scalar table -
// scalar inheritance is disabled in this entry point so existing
// single-package callers keep their pre-scalar-inheritance output.
func GenerateValidatorsPackage(pkg *semantic.Package, outDir string, crossPkg CrossPkg) error {
	return GenerateValidatorsWith(pkg, outDir, crossPkg, nil)
}

// GenerateValidatorsWith is the project-aware entry point: it
// accepts the [ScalarTable] built by [BuildScalarTable] so a field
// typed `Email` (local scalar) or `shared.NonEmptyID` (cross-pkg
// scalar) inherits the scalar's own decorator chain into its
// generated Validate() body.
//
// Used by the multi-package CLI flow; single-package fixtures and
// tests continue calling [GenerateValidators] / [GenerateValidatorsPackage]
// which pass nil for the table.
func GenerateValidatorsWith(pkg *semantic.Package, outDir string, crossPkg CrossPkg, scalars ScalarTable) error {
	if pkg.Name == "" {
		return fmt.Errorf("package has no name")
	}
	pkgDir := filepath.Join(outDir, pkg.Name)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		return err
	}
	data := buildValidateData(pkg, crossPkg, scalars)
	formatted, err := renderGo(tmpl("validate.tmpl"), data)
	if err != nil {
		return fmt.Errorf("render validate.go: %w", err)
	}
	return os.WriteFile(filepath.Join(pkgDir, "validate.go"), formatted, 0o644)
}

// buildValidateData walks every TypeDecl, builds the per-field check
// list, and folds the resulting imports into a single sorted set. Both
// concrete and generic decls produce a Validate(); generics emit with a
// parametric receiver (see [validatorType.TypeParams]).
//
// crossPkg is not consulted here: cross-package fields validate via
// the receiver's own Validate() method, resolved by the import
// already present in types.go.
//
// scalars, when non-nil, enables scalar-decorator inheritance: a
// field whose declared type is a scalar gains the scalar's own
// `@format` / `@length` / `@min` / etc. validators on top of the
// field-level chain. See [scalarInheritedDecorators].
func buildValidateData(pkg *semantic.Package, crossPkg CrossPkg, scalars ScalarTable) validateData {
	_ = crossPkg
	names := make([]string, 0, len(pkg.Types))
	for n := range pkg.Types {
		names = append(names, n)
	}
	sort.Strings(names)

	uses := map[string]bool{}
	var types []validatorType
	for _, name := range names {
		td := pkg.Types[name]
		types = append(types, validatorType{
			Name:       name,
			TypeParams: td.TypeParams,
			Checks:     collectChecks(td, pkg, scalars, uses),
		})
	}

	imps := make([]string, 0, len(uses))
	for k := range uses {
		imps = append(imps, k)
	}
	sort.Strings(imps)

	return validateData{
		Package: pkg.Name,
		Imports: imps,
		Types:   types,
	}
}

// collectChecks returns every Go statement that should land inside a
// type's Validate() body. Empty result means the type compiles into an
// `if-less` Validate() that just returns nil.
//
// Per-field, the order of checks is:
//
//  1. Decorator-driven validators (registry dispatch in validate_registry.go).
//  2. Generic type-parameter fields → runtime type-assertion path.
//  3. Enum-typed fields → auto switch-case validity check.
//  4. User-defined struct fields → recursive `field.Validate()` call.
//
// Steps 2-4 are mutually exclusive: a field is either a typeParam ref,
// an enum, a struct, or a primitive. Primitives reach none of them.
func collectChecks(td *ast.TypeDecl, pkg *semantic.Package, scalars ScalarTable, uses map[string]bool) []string {
	var out []string
	for _, m := range td.Body {
		f, ok := m.(*ast.Field)
		if !ok {
			continue
		}
		out = append(out, fieldChecksWithScalar(f, pkg, scalars, uses)...)
		if isTypeParamRef(f.Type, td.TypeParams) {
			if call := typeParamValidateCall(f); call != "" {
				out = append(out, call)
			}
			continue
		}
		if call := enumValueCheck(f, pkg, uses); call != "" {
			out = append(out, call)
		}
		if nested := nestedValidateCall(f, pkg, uses); nested != "" {
			out = append(out, nested)
		}
	}
	// Type-level cross-field validators (@requiresOneOf,
	// @mutuallyExclusive) run AFTER per-field checks so a clearly-bad
	// individual field surfaces its own error first. The cross-field
	// rules then assume each visible value is structurally sound.
	out = append(out, crossFieldChecks(td, uses)...)
	return out
}

// crossFieldChecks emits the type-level validators @requiresOneOf and
// @mutuallyExclusive. Each takes an array of field names; the
// generated code computes each field's "presence" via [presenceExpr]
// and then asserts the count constraint.
//
//	@requiresOneOf(["a", "b"])     → at least one must be present
//	@mutuallyExclusive(["a", "b"]) → at most one may be present
func crossFieldChecks(td *ast.TypeDecl, uses map[string]bool) []string {
	if len(td.Decorators) == 0 {
		return nil
	}
	var out []string
	for _, d := range td.Decorators {
		switch d.Name {
		case "requiresOneOf":
			if names := stringArrayDecoratorArg(d); len(names) > 0 {
				out = append(out, requiresOneOfCheck(td, names, uses))
			}
		case "mutuallyExclusive":
			if names := stringArrayDecoratorArg(d); len(names) > 0 {
				out = append(out, mutuallyExclusiveCheck(td, names, uses))
			}
		}
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
func requiresOneOfCheck(td *ast.TypeDecl, names []string, uses map[string]bool) string {
	uses["fmt"] = true
	parts := absenceParts(td, names)
	cond := strings.Join(parts, " && ")
	msg := fmt.Sprintf(`"%s: requiresOneOf %v - at least one must be set"`, td.Name, names)
	return ifReturnf(cond, msg)
}

// mutuallyExclusiveCheck emits a counter-based block: count how many
// of the listed fields are present and reject when > 1. The whole
// thing is wrapped in a bare `{ ... }` block so the `n` counter
// scopes locally - multiple @mutuallyExclusive declarations on the
// same struct don't shadow each other.
func mutuallyExclusiveCheck(td *ast.TypeDecl, names []string, uses map[string]bool) string {
	uses["fmt"] = true
	parts := presenceParts(td, names)
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
func presenceParts(td *ast.TypeDecl, names []string) []string {
	parts := make([]string, 0, len(names))
	for _, name := range names {
		f := lookupField(td, name)
		if f == nil {
			parts = append(parts, "false")
			continue
		}
		parts = append(parts, presenceExpr(f))
	}
	return parts
}

// lookupField finds the Field in a TypeDecl by DSL field name.
func lookupField(td *ast.TypeDecl, name string) *ast.Field {
	for _, m := range td.Body {
		f, ok := m.(*ast.Field)
		if ok && f.Name == name {
			return f
		}
	}
	return nil
}

// presenceExpr returns the Go expression that's true when the field
// has a meaningful value (matching @required's definition):
//
//   - optional `T?` (pointer) → `v.X != nil`
//   - slice / map            → `len(v.X) > 0`
//   - string                 → `v.X != ""`
//   - numeric                → `v.X != 0`
//   - other                  → fall back to "true" (always present)
func presenceExpr(f *ast.Field) string {
	access := "v." + GoFieldName(f.Name)
	if f.Type == nil {
		return "true"
	}
	if f.Type.Optional {
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
func absenceParts(td *ast.TypeDecl, names []string) []string {
	parts := make([]string, 0, len(names))
	for _, name := range names {
		f := lookupField(td, name)
		if f == nil {
			// Unknown field → treat as "present" so the rule never
			// fires for typoed names; mirrors presenceParts's
			// `false` literal but on the absence side.
			parts = append(parts, "false")
			continue
		}
		parts = append(parts, absenceExpr(f))
	}
	return parts
}

// absenceExpr is the inverse of [presenceExpr]. Operators are flipped
// directly (`!=` ↔ `==`, `> 0` → `== 0`, `bool` → `!bool`) so the
// generated source is the form `staticcheck` recommends and no extra
// `!(...)` wrapping leaks into the output.
func absenceExpr(f *ast.Field) string {
	access := "v." + GoFieldName(f.Name)
	if f.Type == nil {
		return "false"
	}
	if f.Type.Optional {
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
