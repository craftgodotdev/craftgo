package codegen

import (
	"fmt"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// This file holds the decorator → emit-function dispatch table. The
// rest of the validate codegen is procedural; everything that decides
// "which decorator triggers which Go code" lives here so adding a new
// validator is one entry edit, not three (case label + impl + helper).

// emitCtx is the side-channel state passed to every validator's emit
// function. `uses` collects standard-library imports that the generated
// Validate file needs (`fmt`, `regexp`, ...); `pkg` is forwarded for
// validators that need the symbol table (today only the enum-aware
// path); `regexes` interns regex patterns into package-level vars so
// `regexp.MustCompile` runs ONCE per process instead of per-call.
type emitCtx struct {
	pkg     *semantic.Package
	uses    map[string]bool
	regexes *regexRegistry
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

// validatorEntry binds a decorator name to its emit function. The emit
// signature is uniform so the dispatcher in [fieldChecksWithPkg] can
// stay table-driven: every validator returns the Go source for one
// check, or "" to opt out (type mismatch, missing args, etc.).
type validatorEntry struct {
	name string
	emit func(f *ast.Field, access string, d *ast.Decorator, ctx emitCtx) string
}

// validators is the source-of-truth registry. Order doesn't matter for
// correctness - names are looked up - but the table is grouped by
// concern to make scanning easier: strings/numerics/arrays/files.
//
// Presence ("required") is no longer a decorator - craftgo enforces
// "required by default" and the absence check fires automatically for
// every non-optional field via [fieldChecksWithScalar]. The opt-out is
// the type-level `?` suffix.
var validators = []validatorEntry{
	// string
	{"length", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string { return lengthCheck(f, a, d, c.uses) }},
	{"minLength", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return minMaxLengthCheck(f, a, d, "min", c.uses)
	}},
	{"maxLength", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return minMaxLengthCheck(f, a, d, "max", c.uses)
	}},
	{"pattern", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string { return patternCheck(f, a, d, c) }},
	{"format", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string { return formatCheck(f, a, d, c) }},

	// numeric — math-style comparison operators. Strict variants
	// (@gt, @lt) sit next to inclusive variants (@gte, @lte); no
	// legacy aliases. `@positive`/`@negative` remain as flag-form
	// sugar for `@gt(0)` / `@lt(0)`.
	{"gt", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return numericBoundCheck(f, a, d, ">", "must be greater than", c.uses)
	}},
	{"gte", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return numericBoundCheck(f, a, d, ">=", "below minimum", c.uses)
	}},
	{"lt", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return numericBoundCheck(f, a, d, "<", "must be less than", c.uses)
	}},
	{"lte", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return numericBoundCheck(f, a, d, "<=", "above maximum", c.uses)
	}},
	{"range", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string { return rangeCheck(f, a, d, c.uses) }},
	{"positive", func(f *ast.Field, a string, _ *ast.Decorator, c emitCtx) string {
		return signCheck(f, a, "positive", c.uses)
	}},
	{"negative", func(f *ast.Field, a string, _ *ast.Decorator, c emitCtx) string {
		return signCheck(f, a, "negative", c.uses)
	}},
	{"multipleOf", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return multipleOfCheck(f, a, d, c.uses)
	}},

	// array
	{"minItems", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return itemsBoundCheck(f, a, d, ">=", "minItems", c.uses)
	}},
	{"maxItems", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return itemsBoundCheck(f, a, d, "<=", "maxItems", c.uses)
	}},
	{"uniqueItems", func(f *ast.Field, a string, _ *ast.Decorator, c emitCtx) string {
		return uniqueItemsCheck(f, a, c.uses)
	}},

	// file
	{"maxSize", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string { return maxSizeCheck(f, a, d, c.uses) }},
	{"mimeTypes", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return mimeTypesCheck(f, a, d, c.uses)
	}},
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
func fieldChecksWithScalar(f *ast.Field, pkg *semantic.Package, scalars ScalarTable, ctx emitCtx) []string {
	access := "v." + GoFieldName(f.Name)
	uses := ctx.uses
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
	// null" — the two states are not distinguishable from the
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
	if f.Type != nil && !f.Type.Optional && !hasNullableDecorator(f.Decorators) {
		if s := requiredCheckEnumAware(f, access, pkg, uses); s != "" {
			out = append(out, s)
		}
	}

	// Scalar inheritance: walk the field's TypeRef recursively to
	// find every reachable scalar leaf, then emit one wrapped check
	// per scalar decorator per leaf. Handles arbitrary depth - flat
	// scalar (`email Email`), array (`tags Tag[]`), map (`m
	// map<Tag, V>`), nested array (`tags Tag[][]` once the AST is
	// extended), and nested map (`m map<K, map<Kʹ, Tag>>`) all flow
	// through the same code path.
	for _, leaf := range findScalarLeaves(f.Type, access, 0, scalars) {
		out = append(out, leaf.emitChecks(f, ctx)...)
	}

	// Field-level decorators run on the original field type.
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

// scalarLeaf describes one scalar reached after walking a chain of
// wrappers (Map / Array / Optional) from a field's root TypeRef.
// Each leaf is a self-contained emit unit: the declaration to walk
// (`decl`), the Go expression that accesses one leaf value
// (`access`), whether the leaf is optional (so the synth field
// keeps the pointer flag and `optionalGuard` produces the nil
// check), and a `wrap` closure that nests the per-decorator emit
// body inside the matching for-range / if-not-nil loops.
//
// Each scalar decorator on `decl` produces its own wrapped emit so
// the generated source mirrors the single-validator-per-loop shape
// the rest of the validator emitters use.
type scalarLeaf struct {
	decl     *ast.ScalarDecl
	access   string
	optional bool
	wrap     func(body string) string
}

// emitChecks runs every scalar decorator through the validator
// dispatcher and returns one wrapped emission for the whole leaf.
// All non-empty bodies are concatenated BEFORE the wrap so a map
// with `@minLength @maxLength` on its key plus `@format @maxLength`
// on its value emits exactly two for-loops (one per side) instead
// of one loop per decorator. Empty body strings (when a validator's
// type-guard rejects) are dropped silently.
func (l *scalarLeaf) emitChecks(f *ast.Field, ctx emitCtx) []string {
	synth := scalarLeafSynthField(f, l.decl, l.optional)
	var bodies []string
	for _, d := range l.decl.Decorators {
		v := validatorByName(d.Name)
		if v == nil {
			continue
		}
		body := v.emit(synth, l.access, d, ctx)
		if body == "" {
			continue
		}
		bodies = append(bodies, body)
	}
	if len(bodies) == 0 {
		return nil
	}
	return []string{l.wrap(strings.Join(bodies, "\n"))}
}

// findScalarLeaves walks t recursively, returning every scalar
// reachable through any combination of Map / Array / Optional
// wrappers. The function is value-side complete: it handles
// nested-map (`map<K, map<Kʹ, Tag>>`), array-inside-map
// (`map<K, Tag[]>`), and once the AST supports it, multi-array
// (`Tag[][]`) - every path is one base case (the Named leaf) plus
// three recursion arms (Map / Array / Optional).
//
//   - baseExpr is the Go expression naming the current node
//     (e.g. `v.Tags`, or `val0` after entering a map's value).
//   - depth is incremented at each recursion to keep loop
//     variable names unique across nested layers.
func findScalarLeaves(t *ast.TypeRef, baseExpr string, depth int, scalars ScalarTable) []scalarLeaf {
	if t == nil {
		return nil
	}
	// Map: walk both sides. Map keys can themselves be scalars
	// (typically flat, since Go forbids slice / map keys); map
	// values can be ANY TypeRef including a nested map / array.
	if t.Map != nil {
		var out []scalarLeaf
		keyVar := fmt.Sprintf("k%d", depth)
		valVar := fmt.Sprintf("val%d", depth)
		// Key side. Walk the Map.Key TypeRef with the key loop
		// variable as the new base expression. Wrap result with
		// the outer `for k := range baseExpr` form (no value
		// binding so unused-variable lint stays quiet - Go's
		// for-range key-only form omits the value automatically).
		for _, leaf := range findScalarLeaves(t.Map.Key, keyVar, depth+1, scalars) {
			inner := leaf.wrap
			outer := baseExpr
			leaf.wrap = func(body string) string {
				return fmt.Sprintf("for %s := range %s {\n%s\n}", keyVar, outer, inner(body))
			}
			out = append(out, leaf)
		}
		// Value side. Walk Map.Value with the value loop variable
		// as base. Wrap with `for _, val := range baseExpr` so the
		// blank-identifier key avoids unused-variable issues.
		for _, leaf := range findScalarLeaves(t.Map.Value, valVar, depth+1, scalars) {
			inner := leaf.wrap
			outer := baseExpr
			leaf.wrap = func(body string) string {
				return fmt.Sprintf("for _, %s := range %s {\n%s\n}", valVar, outer, inner(body))
			}
			out = append(out, leaf)
		}
		return out
	}
	// Array: peel ONE bracket per recursion layer so multi-array
	// (`Tag[][]`) builds nested for-loops naturally - the inner
	// recursion sees a TypeRef whose ArrayDepth has been
	// decremented by one. Each layer wraps the inner emit with
	// `for iN := range <prev>`.
	if t.Array || t.ArrayDepth > 0 {
		idxVar := fmt.Sprintf("i%d", depth)
		elemExpr := baseExpr + "[" + idxVar + "]"
		inner := *t
		// Strip one array dimension. Optional on the OUTER slice
		// (`T[]?`) doesn't propagate to the element - for-range
		// handles nil slice silently - so we drop it here.
		if inner.ArrayDepth > 0 {
			inner.ArrayDepth--
		}
		if inner.ArrayDepth == 0 {
			inner.Array = false
		}
		inner.Optional = false
		var out []scalarLeaf
		for _, leaf := range findScalarLeaves(&inner, elemExpr, depth+1, scalars) {
			innerWrap := leaf.wrap
			outer := baseExpr
			leaf.wrap = func(body string) string {
				return fmt.Sprintf("for %s := range %s {\n%s\n}", idxVar, outer, innerWrap(body))
			}
			out = append(out, leaf)
		}
		return out
	}
	// Leaf: a Named TypeRef. The optional flag rides on the leaf
	// so the synth field keeps it (the validator's `optionalGuard`
	// produces the nil-check + deref). Pure-leaf optional isn't a
	// Map/Array; baseExpr is the field's full path.
	if t.Named != nil && t.Named.Name != nil {
		if sd := scalars[t.Named.Name.String()]; sd != nil {
			return []scalarLeaf{{
				decl:     sd,
				access:   baseExpr,
				optional: t.Optional,
				wrap:     func(body string) string { return body },
			}}
		}
	}
	return nil
}

// scalarLeafSynthField builds the synth field used by emit
// helpers when validating one leaf value. The declared type is
// the scalar's underlying primitive; Array / Map flags are off so
// per-element predicates (`isStringOrOptString`, `isNumericField`,
// ...) accept the field; Optional is set to leaf.optional so
// `optionalGuard` / `stringValueExpr` / `numericValueExpr` produce
// the right nil-guard + deref against the leaf's access expr.
func scalarLeafSynthField(f *ast.Field, sd *ast.ScalarDecl, optional bool) *ast.Field {
	cp := *f
	cp.Type = &ast.TypeRef{
		Pos:      f.Type.Pos,
		Optional: optional,
		Named: &ast.NamedTypeRef{
			Pos:  f.Type.Pos,
			Name: &ast.QualifiedIdent{Pos: f.Type.Pos, Parts: []string{scalarPrimitiveDSL(sd.Primitive)}},
		},
	}
	return &cp
}

// scalarPrimitiveDSL maps a scalar's DSL primitive token to the
// canonical name the validator type predicates expect. For most
// primitives (`string`, `int`, ...) this is identity; `bytes` is
// kept verbatim because nothing in the validator set inspects it
// today.
func scalarPrimitiveDSL(name string) string { return name }
