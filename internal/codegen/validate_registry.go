package codegen

import (
	"fmt"

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
// path).
type emitCtx struct {
	pkg  *semantic.Package
	uses map[string]bool
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
	{"pattern", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string { return patternCheck(f, a, d, c.uses) }},
	{"format", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string { return formatCheck(f, a, d, c.uses) }},

	// numeric — math-style comparison operators replace the older
	// @min (= @gte) and @max (= @lte). Strict variants (@gt, @lt)
	// have no @min/@max equivalent; they fill the gap that previously
	// forced users to write @positive / @negative or @pattern.
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
	{"multipleOf", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string { return multipleOfCheck(f, a, d, c.uses) }},

	// array
	{"minItems", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return itemsBoundCheck(f, a, d, ">=", "minItems", c.uses)
	}},
	{"maxItems", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return itemsBoundCheck(f, a, d, "<=", "maxItems", c.uses)
	}},
	{"uniqueItems", func(f *ast.Field, a string, _ *ast.Decorator, c emitCtx) string { return uniqueItemsCheck(f, a, c.uses) }},

	// file
	{"maxSize", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string { return maxSizeCheck(f, a, d, c.uses) }},
	{"mimeTypes", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string { return mimeTypesCheck(f, a, d, c.uses) }},
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
func fieldChecksWithScalar(f *ast.Field, pkg *semantic.Package, scalars ScalarTable, uses map[string]bool) []string {
	access := "v." + GoFieldName(f.Name)
	ctx := emitCtx{pkg: pkg, uses: uses}
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

	// Field-level decorators run on the original field type. `@each`
	// is a meta-decorator: its nested decorator applies to every
	// array element, so we synthesise an element-typed field and
	// dispatch the inner decorator through the same registry.
	for _, d := range f.Decorators {
		if d.Name == "each" {
			if s := eachCheck(f, d, ctx); s != "" {
				out = append(out, s)
			}
			continue
		}
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

// eachCheck emits a per-element validation loop for `@each(@inner)`.
// The inner decorator is dispatched through the regular registry but
// against a synthetic field whose type is the array element (no
// `[]` suffix) and whose access expression is `v.X[i]`. Multi-dim
// arrays (`int[][]`) are not supported in v1 — `@each(@each(...))`
// would be the natural sugar but adds nesting we can revisit later.
func eachCheck(f *ast.Field, d *ast.Decorator, ctx emitCtx) string {
	if f == nil || f.Type == nil || !f.Type.Array || len(d.Args) != 1 || d.Args[0].Nested == nil {
		return ""
	}
	inner := d.Args[0].Nested
	v := validatorByName(inner.Name)
	if v == nil {
		return ""
	}
	// Synthetic element field: drop one array dimension. ArrayDepth
	// > 1 leaves a residual `[]` so the inner emitter still sees an
	// array; users wanting per-leaf semantics on a 2-D array should
	// nest `@each(@each(...))` once that form lands.
	elem := *f.Type
	elem.Array = false
	elem.ArrayDepth = 0
	if f.Type.ArrayDepth > 1 {
		elem.Array = true
		elem.ArrayDepth = f.Type.ArrayDepth - 1
	}
	synth := &ast.Field{Name: f.Name, Type: &elem}
	idx := "_i"
	access := "v." + GoFieldName(f.Name) + "[" + idx + "]"
	body := v.emit(synth, access, inner, ctx)
	if body == "" {
		return ""
	}
	return fmt.Sprintf("for %s := range v.%s {\n%s\n}", idx, GoFieldName(f.Name), body)
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
// dispatcher, emitting one fully-wrapped check per decorator.
// Empty body strings (when the validator's type-guard rejects)
// are dropped.
func (l *scalarLeaf) emitChecks(f *ast.Field, ctx emitCtx) []string {
	synth := scalarLeafSynthField(f, l.decl, l.optional)
	var out []string
	for _, d := range l.decl.Decorators {
		v := validatorByName(d.Name)
		if v == nil {
			continue
		}
		body := v.emit(synth, l.access, d, ctx)
		if body == "" {
			continue
		}
		out = append(out, l.wrap(body))
	}
	return out
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
