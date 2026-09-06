package codegen

import (
	"fmt"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/strfmt"
	"github.com/getkin/kin-openapi/openapi3"
)

// This file holds the per-decorator emit table: the runtime check and
// the OpenAPI keyword each constraint decorator produces. The
// rest of the validate codegen is procedural; everything that decides
// "which decorator triggers which Go code" lives here so adding a new
// validator is one entry edit, not three (case label + impl + helper).

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
	resolver *ProjectResolver
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

// oasFamily groups the OpenAPI keywords a constraint decorator maps to,
// so an emit site can stamp a subset: the map-key propertyNames builder
// omits numeric bounds no SDK generator honours, and the multipart part
// schema applies only the item counts.
type oasFamily uint8

const (
	oasNumeric oasFamily = 1 << iota // minimum / maximum / exclusive* / multipleOf
	oasLength                        // minLength / maxLength
	oasText                          // pattern / format
	oasArray                         // minItems / maxItems / uniqueItems (or the *Properties twins)

	oasAll = oasNumeric | oasLength | oasText | oasArray
	// oasNarrowing is every family a field can stack on a referenced
	// type; a $ref field is never an array or a file.
	oasNarrowing = oasNumeric | oasLength | oasText
)

// validatorEntry is the emit row for one constraint decorator: the runtime
// check it compiles to and the OpenAPI keyword it advertises. The emit
// signature is uniform so the dispatcher in [fieldChecksWithScalar] can
// stay table-driven: every validator returns the Go source for one check,
// or "" to opt out (type mismatch, missing args, etc.). oas stamps the
// decorator's keyword onto a schema; nil when the decorator has no
// OpenAPI form (the file validators).
type validatorEntry struct {
	name   string
	emit   func(f *ast.Field, access string, d *ast.Decorator, ctx emitCtx) string
	family oasFamily
	oas    func(d *ast.Decorator, s *openapi3.Schema)
}

// validators is the source-of-truth registry. Order doesn't matter for
// correctness - names are looked up - but the table is grouped by
// concern to make scanning easier: strings/numerics/arrays/files.
//
// Presence ("required") is not a decorator - craftgo enforces
// "required by default" and the absence check fires automatically for
// every non-optional field via [fieldChecksWithScalar]. The opt-out is
// the type-level `?` suffix.
var validators = []validatorEntry{
	// string
	{"length", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string { return lengthCheck(f, a, d, c) },
		oasLength, func(d *ast.Decorator, s *openapi3.Schema) {
			if !lengthKeywordsApply(s) {
				return
			}
			// `@length(N)` is exact length - fold the single argument into
			// both bounds (min == max == N), matching the runtime check and
			// the map-key path; `@length(min, max)` is a range.
			lo, ok := numericArgValue(d, 0)
			if !ok || lo < 0 {
				return
			}
			hi := lo
			if v, ok := numericArgValue(d, 1); ok && v >= 0 {
				hi = v
			}
			setMinLen(s, uint64(lo))
			setMaxLen(s, uint64(hi))
		}},
	{"minLength", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return minMaxLengthCheck(f, a, d, "min", c)
	}, oasLength, func(d *ast.Decorator, s *openapi3.Schema) {
		if v, ok := numericArgValue(d, 0); ok && v >= 0 && lengthKeywordsApply(s) {
			setMinLen(s, uint64(v))
		}
	}},
	{"maxLength", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return minMaxLengthCheck(f, a, d, "max", c)
	}, oasLength, func(d *ast.Decorator, s *openapi3.Schema) {
		if v, ok := numericArgValue(d, 0); ok && v >= 0 && lengthKeywordsApply(s) {
			setMaxLen(s, uint64(v))
		}
	}},
	{"pattern", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string { return patternCheck(f, a, d, c) },
		oasText, func(d *ast.Decorator, s *openapi3.Schema) {
			if len(d.Args) == 1 {
				if sl, ok := d.Args[0].Value.(*ast.StringLit); ok {
					s.Pattern = sl.Value
				}
			}
		}},
	{"format", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string { return formatCheck(f, a, d, c) },
		oasText, func(d *ast.Decorator, s *openapi3.Schema) {
			if len(d.Args) != 1 {
				return
			}
			switch v := d.Args[0].Value.(type) {
			case *ast.StringLit:
				s.Format = strfmt.OpenAPIFormat(v.Value)
			case *ast.IdentExpr:
				if v.Name != nil {
					s.Format = strfmt.OpenAPIFormat(v.Name.String())
				}
			}
		}},

	// numeric - math-style comparison operators. Strict variants
	// (@gt, @lt) sit next to inclusive variants (@gte, @lte); no
	// legacy aliases. `@positive`/`@negative` remain as flag-form
	// sugar for `@gt(0)` / `@lt(0)`.
	{"gt", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return numericBoundCheck(f, a, d, ">", "must be greater than", c)
	}, oasNumeric, func(d *ast.Decorator, s *openapi3.Schema) { emitExclusive(s, "exclusiveMinimum", d, 0) }},
	{"gte", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return numericBoundCheck(f, a, d, ">=", "below minimum", c)
	}, oasNumeric, func(d *ast.Decorator, s *openapi3.Schema) { emitBound(s, "minimum", d, 0, setMin) }},
	{"lt", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return numericBoundCheck(f, a, d, "<", "must be less than", c)
	}, oasNumeric, func(d *ast.Decorator, s *openapi3.Schema) { emitExclusive(s, "exclusiveMaximum", d, 0) }},
	{"lte", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return numericBoundCheck(f, a, d, "<=", "above maximum", c)
	}, oasNumeric, func(d *ast.Decorator, s *openapi3.Schema) { emitBound(s, "maximum", d, 0, setMax) }},
	{"range", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string { return rangeCheck(f, a, d, c) },
		oasNumeric, func(d *ast.Decorator, s *openapi3.Schema) {
			emitBound(s, "minimum", d, 0, setMin)
			emitBound(s, "maximum", d, 1, setMax)
		}},
	{"positive", func(f *ast.Field, a string, _ *ast.Decorator, c emitCtx) string {
		return signCheck(f, a, "positive", c)
	}, oasNumeric, func(_ *ast.Decorator, s *openapi3.Schema) { setExclusive(s, "exclusiveMinimum", 0, nil) }},
	{"negative", func(f *ast.Field, a string, _ *ast.Decorator, c emitCtx) string {
		return signCheck(f, a, "negative", c)
	}, oasNumeric, func(_ *ast.Decorator, s *openapi3.Schema) { setExclusive(s, "exclusiveMaximum", 0, nil) }},
	{"multipleOf", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return multipleOfCheck(f, a, d, c)
	}, oasNumeric, func(d *ast.Decorator, s *openapi3.Schema) {
		if r, ok := rawIfBigInt(d, 0); ok {
			schemaExt(s, "multipleOf", r)
		} else if v, ok := numericArgValue(d, 0); ok && v != 0 {
			s.MultipleOf = &v
		}
	}},

	// array
	{"minItems", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return itemsBoundCheck(f, a, d, ">=", "minItems", c)
	}, oasArray, func(d *ast.Decorator, s *openapi3.Schema) {
		itemCountKeyword(s, d, func(u uint64) { s.MinItems = u }, func(u uint64) { s.MinProps = u })
	}},
	{"maxItems", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return itemsBoundCheck(f, a, d, "<=", "maxItems", c)
	}, oasArray, func(d *ast.Decorator, s *openapi3.Schema) {
		itemCountKeyword(s, d, func(u uint64) { s.MaxItems = &u }, func(u uint64) { s.MaxProps = &u })
	}},
	{"uniqueItems", func(f *ast.Field, a string, _ *ast.Decorator, c emitCtx) string {
		return uniqueItemsCheck(f, a, c)
	}, oasArray, func(_ *ast.Decorator, s *openapi3.Schema) {
		if s.Type != nil && s.Type.Includes("array") {
			s.UniqueItems = true
		}
	}},

	// file - runtime only; a multipart part has no schema keyword for these.
	{"maxSize", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string { return maxSizeCheck(f, a, d, c) }, 0, nil},
	{"mimeTypes", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return mimeTypesCheck(f, a, d, c)
	}, 0, nil},
}

// applyFieldConstraints stamps every constraint keyword a field or scalar
// schema can carry onto s - numeric bounds, string length, pattern /
// format, and item counts. Each row acts only when its decorator is
// present, and the semantic layer guarantees those are type-appropriate,
// so applying the whole table is safe everywhere and "which constraints a
// schema gets" is decided in ONE place. The map-KEY propertyNames builder
// is the deliberate exception - it omits numeric bounds no SDK generator
// honours - so it applies a subset through [applyConstraintFamilies].
func applyFieldConstraints(ds []*ast.Decorator, s *openapi3.Schema) {
	applyConstraintFamilies(ds, s, oasAll)
}

// applyConstraintFamilies stamps the keywords of the decorators in ds whose
// family is in fams onto s.
func applyConstraintFamilies(ds []*ast.Decorator, s *openapi3.Schema, fams oasFamily) {
	if s == nil {
		return
	}
	for _, d := range ds {
		if d == nil {
			continue
		}
		if v := validatorByName(d.Name); v != nil && v.oas != nil && v.family&fams != 0 {
			v.oas(d, s)
		}
	}
}

// hasFieldConstraintDecorator reports whether ds carries a decorator that
// narrows a referenced type with an OpenAPI validation keyword.
func hasFieldConstraintDecorator(ds []*ast.Decorator) bool {
	for _, d := range ds {
		if d == nil {
			continue
		}
		if v := validatorByName(d.Name); v != nil && v.oas != nil && v.family&oasNarrowing != 0 {
			return true
		}
	}
	return false
}

// validatorByName returns the registry entry for `name`, or nil when the
// decorator isn't a recognised validator (metadata decorators like
// `@doc` / `@deprecated` / `@example` fall through here and produce no
// runtime check).
func validatorByName(name string) *validatorEntry {
	for i := range validators {
		if validators[i].name == name {
			return &validators[i]
		}
	}
	return nil
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
		v := validatorByName(d.Name)
		if v == nil {
			continue
		}
		if s := v.emit(f, access, d, ctx); s != "" {
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
