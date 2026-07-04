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

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// validateData is the template input for `validate.tmpl`. It is computed
// up front so the template stays declarative - every conditional is
// resolved in Go code where unit tests can pin behaviour.
type validateData struct {
	Package string
	Imports []string
	// RegexVars are package-level `var` declarations that compile
	// every `@pattern` regex and the regex-backed `@format` patterns
	// ONCE per process. Inline `regexp.MustCompile(...)` inside
	// Validate() would pay the parser cost on every call. Each unique
	// pattern is interned once; duplicates across types share the
	// same var.
	RegexVars []regexVar
	Types     []validatorType
	// NeedsValidateValue emits the package-level `validateValue` reflection
	// helper. It is the fallback for a generic type-parameter field whose
	// argument is a composite (`Page<map<string, Item>>`): the direct
	// `any(x).(Validate)` probe can't reach the element's Validate(), so the
	// helper walks slices / maps / pointers and validates each leaf. Only
	// emitted when a type-param probe is generated (keyed on the `reflect`
	// import), so non-generic packages stay reflection-free.
	NeedsValidateValue bool
}

// regexVar binds a pattern to its package-level Go identifier. Used
// by the template's `var (...)` block.
type regexVar struct {
	Name    string
	Pattern string
}

// validatorType is one Validate() method block in `validate.tmpl`.
// TypeParams is non-empty for generic decls - the template uses it to
// build the receiver suffix `[T any, ...]` so the method is declared on
// the parametric type itself, e.g. `func (v *Page[T]) Validate() error`.
//
// PtrReceiver picks the receiver form. Structs / generics / error
// bodies validate through a `*T` receiver, which matches the rest of
// the generated API and lets the generic type-assertion probe
// `any(&elem)` find them. Scalars and enums are defined types whose
// Validate() takes a VALUE receiver `func (v Email) Validate()` so the
// body can cast the receiver to its primitive (`string(v)`) and so a
// non-addressable map-range copy (`for _, val := range m {
// val.Validate() }`) can call it. A value-receiver method is in both
// the `T` and `*T` method sets, so the `any(&elem)` probe still
// resolves it for generic instances.
type validatorType struct {
	Name        string
	TypeParams  []string
	Checks      []string
	PtrReceiver bool
}

// GenerateValidators writes `validate.go` next to `types.go`. The file
// adds a `Validate() error` method to every concrete TypeDecl. Types
// without any constraints get an empty stub so handlers can call
// `req.Validate()` uniformly.
//
// Equivalent to [GenerateValidatorsPackage] with a nil [CrossPkg]
// context, for single-package callers and tests.
func GenerateValidators(pkg *semantic.Package, outDir string) error {
	return GenerateValidatorsPackage(pkg, outDir, nil)
}

// GenerateValidatorsPackage is the multi-package variant of
// [GenerateValidators]. crossPkg adds Go imports for every cross-
// package alias used in pkg's field types so `req.User.Validate()`
// can dispatch to the sibling package's validator.
//
// Equivalent to [GenerateValidatorsWith] with a nil scalar table:
// scalar inheritance is disabled in this entry point.
func GenerateValidatorsPackage(pkg *semantic.Package, outDir string, crossPkg CrossPkg) error {
	return GenerateValidatorsWith(pkg, outDir, crossPkg, nil, nil)
}

// GenerateValidatorsWith is the project-aware entry point: it
// accepts the [ScalarTable] built by [BuildScalarTable] so a field
// typed `Email` (local scalar) or `shared.NonEmptyID` (cross-pkg
// scalar) inherits the scalar's own decorator chain into its
// generated Validate() body. The [TypeTable] resolves qualified
// type refs (`shared.Page<T>`), which the local-only `pkg.Types`
// lookup cannot reach, so they emit recursive `.Validate()` calls.
//
// Used by the multi-package CLI flow; single-package fixtures and
// tests continue calling [GenerateValidators] / [GenerateValidatorsPackage]
// which pass nil for the tables.
func GenerateValidatorsWith(pkg *semantic.Package, outDir string, crossPkg CrossPkg, scalars ScalarTable, types TypeTable) error {
	return GenerateValidatorsAll(pkg, outDir, crossPkg, scalars, types, nil)
}

// GenerateValidatorsAll is the explicit-tables entry point for tests
// that build tables directly; [GenerateValidatorsResolved] accepts a
// single [ProjectResolver] instead of four ad-hoc tables. This wrapper
// assembles a resolver from the parameters and delegates.
func GenerateValidatorsAll(pkg *semantic.Package, outDir string, crossPkg CrossPkg, scalars ScalarTable, types TypeTable, enums EnumTable) error {
	r := &ProjectResolver{
		Types:    types,
		Enums:    enums,
		Scalars:  scalars,
		CrossPkg: crossPkg,
	}
	return GenerateValidatorsResolved(pkg, outDir, r)
}

// GenerateValidatorsResolved is the canonical entry point. It takes a
// single [ProjectResolver] carrying every cross-package lookup the
// validator emit chain needs - scalar inheritance, generic Validate
// dispatch, cross-pkg enum value-set checks, and the matching Go
// import registrations. nil resolver is tolerated and degrades to
// local-only behaviour, matching the legacy single-package shape.
func GenerateValidatorsResolved(pkg *semantic.Package, outDir string, r *ProjectResolver) error {
	if pkg.Name == "" {
		return fmt.Errorf("package has no name")
	}
	pkgDir := filepath.Join(outDir, pkg.Name)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		return err
	}
	data := buildValidateData(pkg, r)
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
// Cross-package fields validate via the receiver's own Validate()
// method, resolved by the import already present in types.go - no
// CrossPkg parameter is needed here.
//
// scalars, when non-nil, enables scalar-decorator inheritance: a
// field whose declared type is a scalar gains the scalar's own
// `@format` / `@length` / `@min` / etc. validators on top of the
// field-level chain. See [scalarInheritedDecorators].
func buildValidateData(pkg *semantic.Package, r *ProjectResolver) validateData {
	names := sortedKeys(pkg.Types)

	uses := map[string]bool{}
	regexes := newRegexRegistry()
	ctx := emitCtx{pkg: pkg, uses: uses, regexes: regexes, resolver: r}
	var types []validatorType
	for _, name := range names {
		td := pkg.Types[name]
		types = append(types, validatorType{
			Name:        name,
			TypeParams:  td.TypeParams,
			Checks:      collectChecks(td, pkg, r, ctx),
			PtrReceiver: true,
		})
	}

	// Scalar / enum Validate() methods. Each constrained scalar
	// (`scalar Email string @format(email)`) and every enum gets ONE
	// Validate() method carrying the value-set / format / range checks
	// declared on the type. Fields typed as that scalar / enum then
	// dispatch through `v.Field.Validate()` (see [nestedValidateCall]),
	// so the checks are declared once rather than inlined at every use
	// site. Generic instances (`Page[Email]` / `Page[Color]`) validate
	// their elements through the runtime `interface{ Validate() error }`
	// probe, which only finds a method when one actually exists on the
	// element type.
	for _, name := range sortedKeys(pkg.Scalars) {
		sd := pkg.Scalars[name]
		if !scalarDeclHasValidators(sd) {
			continue
		}
		types = append(types, validatorType{
			Name:        name,
			Checks:      scalarValidateChecks(sd, ctx),
			PtrReceiver: false,
		})
	}
	for _, name := range sortedKeys(pkg.Enums) {
		ed := pkg.Enums[name]
		checks := enumValidateChecks(ed)
		if len(checks) > 0 {
			uses["fmt"] = true
		}
		types = append(types, validatorType{
			Name:        name,
			Checks:      checks,
			PtrReceiver: false,
		})
	}

	// Errors with a custom body get their own `<Name>Body` Validate()
	// so per-field decorators (`@minLength`, `@format`, `@gte` ...) on
	// error-body fields fire at runtime, the same as any other type's
	// fields.
	for _, name := range sortedKeys(pkg.Errors) {
		ed := pkg.Errors[name]
		// Carry both direct fields and embedded mixins into the synthetic
		// body type so collectChecks runs the mixin's own Validate() - the
		// error body struct embeds the mixin and the OpenAPI allOf advertises
		// its constrained fields, so the validator must check them too, the
		// same as any other type that embeds a mixin.
		body := &ast.TypeDecl{Name: name + "Body"}
		for _, m := range ed.Body {
			switch m.(type) {
			case *ast.Field, *ast.Mixin:
				body.Body = append(body.Body, m)
			}
		}
		if len(body.Body) == 0 {
			continue
		}
		types = append(types, validatorType{
			Name:        body.Name,
			Checks:      collectChecks(body, pkg, r, ctx),
			PtrReceiver: true,
		})
	}

	imps := make([]string, 0, len(uses))
	for k := range uses {
		imps = append(imps, k)
	}
	sort.Strings(imps)

	return validateData{
		Package:            pkg.Name,
		Imports:            imps,
		RegexVars:          regexes.entries,
		Types:              types,
		NeedsValidateValue: uses["reflect"],
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
//  3. Fields whose type carries a Validate() - user structs, generic
//     instances, enums, and constrained scalars → recursive
//     `field.Validate()` call (see [nestedValidateCall]).
//
// Steps 2-3 are mutually exclusive: a field is either a typeParam ref,
// a Validate()-carrying named type, or a plain primitive. Primitives
// reach neither.
func collectChecks(td *ast.TypeDecl, pkg *semantic.Package, r *ProjectResolver, ctx emitCtx) []string {
	var out []string
	// Dedup the Go field identifiers exactly as the struct renderer does, so
	// the validator reads `v.UserID` / `v.UserID_2` - the same fields the
	// struct declares - rather than `v.UserID` twice for a colliding pair.
	levelNames := resolvedGoFieldNames(td.Body)
	fieldIdx := 0
	for _, m := range td.Body {
		switch v := m.(type) {
		case *ast.Field:
			goName := levelNames[fieldIdx]
			fieldIdx++
			out = append(out, fieldChecksWithScalar(v, goName, pkg, ctx)...)
			if isTypeParamRef(v.Type, td.TypeParams) {
				if call := typeParamValidateCall(v, goName, ctx.uses); call != "" {
					out = append(out, call)
				}
				continue
			}
			// Enum value-set checks and scalar format/range/length
			// checks both dispatch through nestedValidateCall: the
			// constraints live on the scalar's / enum's own Validate()
			// method, and the field calls it (`v.Status.Validate()`).
			// This keeps the check declared once and lets generic
			// instances over a scalar / enum validate their elements.
			if nested := nestedValidateCall(v, goName, pkg, r); nested != "" {
				out = append(out, nested)
			}
		case *ast.Mixin:
			// Embedded mixin: Go's field-promotion exposes the
			// embedded fields directly on the host, but the
			// embedded type's own Validate() method only fires
			// when the host calls it, so the host dispatches to it
			// explicitly to run the checks declared on the mixin's
			// fields. The embedded field's Go name is the last
			// segment of the mixin reference (`shared.Audit`
			// embeds as `Audit`).
			if call := mixinValidateCall(v); call != "" {
				out = append(out, call)
			}
		}
	}
	// Type-level cross-field validators (@requiresOneOf,
	// @mutuallyExclusive) run AFTER per-field checks so a clearly-bad
	// individual field surfaces its own error first. The cross-field
	// rules then assume each visible value is structurally sound.
	out = append(out, crossFieldChecks(td, pkg, r, ctx.uses)...)
	return out
}

// mixinValidateCall emits the recursive Validate() call for an
// embedded mixin. Returns "" when the mixin reference is malformed
// (no parts) so the caller silently skips rather than emit broken Go.
func mixinValidateCall(m *ast.Mixin) string {
	if m == nil || m.Ref == nil || m.Ref.Name == nil || len(m.Ref.Name.Parts) == 0 {
		return ""
	}
	last := m.Ref.Name.Parts[len(m.Ref.Name.Parts)-1]
	return fmt.Sprintf("if err := v.%s.Validate(); err != nil {\nreturn err\n}", last)
}
