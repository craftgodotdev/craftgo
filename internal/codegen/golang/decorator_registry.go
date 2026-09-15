package golang

import (
	"fmt"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// The emit context the Go constraint checks are rendered with. The
// catalogue itself is split: [semantic.Registry] says which decorators
// are constraints and what each restricts, [goChecks] renders the Go
// check, and [schemaKeywords] renders the OpenAPI keyword.

// emitCtx is the side-channel state passed to every validator's emit
// function. `uses` collects imports the generated Validate file needs
// (stdlib `fmt`, `regexp`, ...; also cross-package paths when a
// qualified enum case-list lands in the emitted source); `pkg` is the
// local symbol table; `resolver` is the project-wide lookup
// (cross-pkg enums, types, scalars, errors + import paths) - emit
// sites that need qualified-name resolution route through it instead
// of grabbing individual tables. `regexes` interns regex patterns
// into package-level vars so `regexp.MustCompile` runs ONCE per
// process instead of per-call.
type emitCtx struct {
	pkg      *semantic.Package
	uses     map[string]bool
	regexes  *regexRegistry
	resolver *projectResolver
}

// regexRegistry interns unique regex patterns and assigns each a
// stable Go identifier (`_pattern0`, `_pattern1`, ...). The resulting
// var block is emitted at the top of the generated `validate.go`
// (template's `var (...)` section) so every Validate() call references
// the precompiled regex instead of re-parsing on the hot path.
type regexRegistry struct {
	byPattern map[string]string
	entries   []regexVar
}

// newRegexRegistry returns an empty registry. Callers carry it on
// [emitCtx.regexes] and pass the populated [regexVar] slice into
// [validateData.RegexVars] when rendering the template.
func newRegexRegistry() *regexRegistry {
	return &regexRegistry{byPattern: map[string]string{}}
}

// intern returns the Go identifier bound to `pattern`. First call for
// a given pattern allocates a new ident (`_pattern<N>`); repeats reuse
// the same name. Empty patterns return "" so callers can fall back
// to whatever shape they had before (defensive).
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

// fieldChecksWithScalar dispatches each decorator on a field through
// the validator registry. The lookup is by name; the matched
// validator's `emit` closure is responsible for type-guarding (skip
// silently when the field type doesn't fit) and producing the Go
// source for the check.
//
// Adding a new decorator-driven validator is a single new entry in
// `validators` (see [validatorEntry]) - no edits to this dispatcher.
//
// Scalar inheritance: when the field's declared type matches a
// scalar in `scalars`, the scalar's own decorator chain inherits
// into the field's effective validator list - so a field declared
// `email Email` (where `scalar Email string @format(email)
// @maxLength(254)`) gets the @format + @maxLength checks for free.
// The inherited decorators run BEFORE the field-level chain so
// the emitted source matches author intent (scalar invariants
// enforced first, then per-field overrides). For each scalar
// decorator the emitter sees a synthesised field whose declared
// type is the scalar's underlying primitive - that lets the
// existing type predicates (`isStringOrOptString`,
// `isNumericField`, ...) match without special-casing scalar-typed
// fields throughout the emitter set.
func fieldChecksWithScalar(f *ast.Field, goName string, pkg *semantic.Package, ctx emitCtx) []string {
	access := "v." + goName
	var out []string

	// "Required by default": every non-optional field gets the
	// presence check automatically. Two opt-outs:
	//
	//   - the type-level `?` suffix (field is explicitly optional)
	//   - the `@nullable` decorator (the value may be null on the
	//     wire, which decodes to a nil pointer and SHOULD be
	//     accepted - rejecting nil here would defeat the decorator)
	//
	// Note on `@nullable` and JSON-wire presence: the spec model is
	// "must send key, value may be null", which OpenAPI captures
	// faithfully (the field stays in `required[]` AND carries
	// `nullable: true`). Encoding-side though, Go's JSON decoder
	// produces a nil pointer for both "key missing" and "key set to
	// null" - the two states are not distinguishable from the
	// post-decode struct. Enforcing "key must be present" requires a
	// `json.RawMessage` receiver (or a custom presence tracker),
	// which would change the field's Go-side type from `*T` to a
	// raw-bytes shape and break every user-side accessor. We accept
	// the limitation: OpenAPI carries the contract, generated TS /
	// Java clients enforce sending the key, and non-conforming
	// callers are treated as if they sent explicit null.
	//
	// requiredCheckEnumAware returns "" when the field type has no
	// defined empty value, so primitives the JSON decoder already
	// rejects-on-null get no validate-time block.
	if resolveField(f, pkg, ctx.resolver).RuntimeEnforced {
		if s := requiredCheckEnumAware(f, access, ctx); s != "" {
			out = append(out, s)
		}
	}

	// A constrained scalar carries its `@format` / `@length` / `@min`
	// checks on its OWN Validate() method; the field dispatches through
	// `v.Field.Validate()` (see [nestedValidateCall] and
	// [scalarValidateChecks]). Routing through the scalar's own method
	// keeps the check declared once and reaches scalar elements inside a
	// generic instance, whose Validate() is parametric.

	// Field-level decorators (the ones declared on THIS field, on top of
	// any the scalar type already carries). When the field's type is a
	// scalar, route them through scalarFieldLevelChecks: a defined-type
	// scalar (`type Cents int`) fails the numeric/string type-guards, so
	// emitting against the raw field would not match any validator. The
	// helper casts the value to its primitive in a local first. Non-scalar
	// fields keep the direct path.
	// Scalar AND enum fields route their field-level decorators through
	// scalarFieldLevelChecks: both are defined types whose name fails the
	// validator type-guards, so the helper casts the value to its primitive
	// (int / string) in a local first. Without this, a constraint advertised
	// in OpenAPI (`p Priority @lte(5)`) would never be enforced at runtime.
	prim := scalarFieldPrimitive(f, ctx)
	if prim == "" {
		prim = enumFieldPrimitive(f, ctx)
	}
	if prim != "" {
		if blk := scalarFieldLevelChecks(f, access, prim, ctx); blk != "" {
			out = append(out, blk)
		}
		return out
	}
	for _, d := range f.Decorators {
		check := goChecks[d.Name]
		if check == nil {
			continue
		}
		if s := check(f, access, d, ctx); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// scalarPrimitiveDSL maps a scalar's DSL primitive token to the
// canonical name the validator type predicates expect. For most
// primitives (`string`, `int`, ...) this is identity; `bytes` is
// kept verbatim because nothing in the validator set inspects it
// today.
func scalarPrimitiveDSL(name string) string { return name }
