package semantic

// Numeric value-range and combination checks. Sits between the
// argument-shape pass (kind-correct, count-correct) and codegen, so
// every numeric pair we observe here is well-formed AST. We catch:
//
//   - `@length(min, max)`, `@range(min, max)` - min must be ≤ max.
//   - `@minLength` paired with `@maxLength` on the same field - same
//     ordering rule applied across decorators.
//   - `@minItems` / `@maxItems` pair - likewise.
//   - `@gte` / `@lte` numeric bound pair (and the strict `@gt` / `@lt`
//     and mixed combinations) - the lower bound must be ≤ the upper.
//   - `@multipleOf(0)` - divides nothing; codegen would emit a runtime
//     %0 panic.
//   - `@status(code)` - must be in 100..599 (HTTP status range).
//   - Duration / size literals - must be > 0 (timeout 0s is a footgun;
//     0-byte cap rejects every request).
//   - `@nullable` on `T?` field - redundant per README §"Field
//     presence semantics" (warning, not error).

import (
	"fmt"
	"math"
	"strconv"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// checkRangesAndExtras runs every per-decorator value sanity rule plus
// the cross-decorator pair checks. Called after the args pass so we
// know each decorator's positional count and kinds are sound.
func (a *analyzer) checkRangesAndExtras(files []*ast.File) {
	for _, f := range files {
		for _, d := range f.Decls {
			a.checkDeclRanges(d)
		}
	}
}

// checkDeclRanges dispatches by declaration kind. Type / error bodies
// are walked field-by-field; service methods get their own dispatch.
func (a *analyzer) checkDeclRanges(d ast.Decl) {
	switch dd := d.(type) {
	case *ast.TypeDecl:
		a.checkBodyRanges(dd.Body, dd.TypeParams)
	case *ast.ErrorDecl:
		a.checkBodyRanges(dd.Body, nil)
	case *ast.ScalarDecl:
		a.checkDecoratorRanges(dd.Decorators)
		// A scalar's bound decorators are inherited into the validator
		// of every field that uses it, so the float-on-integer check
		// must run on the scalar declaration as well as on plain fields.
		a.checkIntBoundFloatLiteral(dd.Primitive, fmt.Sprintf("scalar %q", dd.Name), dd.Decorators)
		// The capacity-overflow and unsigned-contradiction checks are
		// otherwise field-only, so a scalar carrying an out-of-range bound
		// (`scalar X uint8 @lte(300)`) or an always-false bound (`scalar X
		// uint @lt(0)`) slipped through and generated non-compiling /
		// reject-everything Go. Run them via a synthetic field typed as the
		// scalar's primitive — exactly the decorators a using field inherits.
		scalarAsField := &ast.Field{
			Name:       dd.Name,
			Type:       &ast.TypeRef{Named: &ast.NamedTypeRef{Pos: dd.Pos, Name: &ast.QualifiedIdent{Pos: dd.Pos, Parts: []string{dd.Primitive}}}},
			Decorators: dd.Decorators,
		}
		a.checkBoundCapacity(scalarAsField)
		a.checkNegativeOnUnsigned(scalarAsField)
		// Pair-ordering (@gte/@lte, @gt/@lt, @minLength/@maxLength,
		// @minItems/@maxItems) is purely structural — it reads the
		// decorators' own numeric args — so a contradictory scalar bound
		// (`scalar Score int @gte(100) @lte(10)`) must be caught here too,
		// not only on fields.
		a.checkPairOrdering(scalarAsField)
		if dd.Primitive == "bytes" {
			// Same rule as a bytes field: @pattern / @format constrain text,
			// not a binary value, so the validator drops them while OpenAPI
			// advertises them. Caught here too because a scalar carries its
			// decorators on the declaration, not the using field.
			for _, d := range dd.Decorators {
				if d != nil && (d.Name == "pattern" || d.Name == "format") {
					a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
						"@%s applies to text, not a `bytes` scalar — a binary value has no string pattern / format. Use a `string` scalar, or drop the decorator.",
						d.Name)
				}
			}
		}
		// Same rules as a field's @multipleOf, applied on the declaration
		// because a scalar carries its decorators here, not on the using
		// field: a float scalar can't use Go's integer-only modulus at all,
		// and an integer scalar needs a whole-number divisor or the OpenAPI
		// advertises a bound the validator drops.
		isFloat := dd.Primitive == "float32" || dd.Primitive == "float64"
		for _, d := range dd.Decorators {
			if d == nil || d.Name != "multipleOf" {
				continue
			}
			if isFloat {
				a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
					"@multipleOf does not support float scalars — Go's modulus operator is integer-only. Use an integer scalar, or add a tolerance check in your handler.")
				continue
			}
			if integerPrim(dd.Primitive) && len(d.Args) == 1 {
				if fl, ok := d.Args[0].Value.(*ast.FloatLit); ok && fl.Value != float64(int64(fl.Value)) {
					a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
						"@multipleOf on an integer scalar needs a whole-number divisor — Go's modulus is integer-only, so a fractional divisor can't be enforced (the OpenAPI would advertise a bound the validator drops). Use a whole number.")
				}
			}
		}
	case *ast.ServiceDecl:
		for _, m := range dd.Methods() {
			a.checkDecoratorRanges(m.Decorators)
		}
	}
}

// checkBodyRanges runs the per-field combination rules in addition to
// the per-decorator value checks. Mixin members are skipped.
func (a *analyzer) checkBodyRanges(members []ast.TypeMember, typeParams []string) {
	for _, m := range members {
		f, ok := m.(*ast.Field)
		if !ok {
			continue
		}
		a.checkDecoratorRanges(f.Decorators)
		a.checkPairOrdering(f)
		a.checkNullableRedundant(f)
		a.checkBoundCapacity(f)
		a.checkBoundLiteralKind(f)
		a.checkMultipleOfTarget(f)
		a.checkNegativeOnUnsigned(f)
		a.checkUniqueItemsComparable(f, typeParams)
		a.checkValueConstraintOnTypeParam(f, typeParams)
		a.checkPatternFormatOnBytes(f)
		a.checkMapKeyComparable(f, typeParams)
	}
}

// checkMapKeyComparable rejects a map whose key type cannot be a Go map
// key: a bare generic type-parameter (`any`-constrained) or a struct /
// generic that transitively contains a slice / map / bytes. The generated
// `map[K]V` does not compile, yet gen and OpenAPI accept the design (gen
// exits 0, then `go build` fails). Mirrors the @uniqueItems comparability
// guard for the map-key position. Walks nested maps / arrays in the
// field's own type; named types are checked when their own body is walked.
func (a *analyzer) checkMapKeyComparable(f *ast.Field, typeParams []string) {
	if f == nil {
		return
	}
	a.mapKeysComparable(f.Type, f, typeParams)
}

func (a *analyzer) mapKeysComparable(t *ast.TypeRef, f *ast.Field, typeParams []string) {
	if t == nil {
		return
	}
	if t.Map != nil {
		if !a.keyMarshalable(t.Map.Key, typeParams) {
			a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeMapKeyType,
				"map key %s is not a usable map key: a JSON object key is a string, so encoding/json supports only a string / int* / uint* key (or a scalar / enum over one). A bool, float, struct, slice, map, bytes, or generic type-parameter key either fails to compile or panics at json.Marshal. Use a string / int* / uint* / string- or int-scalar / enum key.",
				describeTypeRef(t.Map.Key))
		}
		a.mapKeysComparable(t.Map.Value, f, typeParams)
		a.mapKeysComparable(t.Map.Key, f, typeParams)
		return
	}
	if t.Array {
		a.mapKeysComparable(peelOneArray(t), f, typeParams)
		return
	}
	// A generic instance (`Box<map<bad, V>>`) carries the map inside its
	// type-arg; descend so a non-marshalable key nested in a type-argument is
	// caught too, mirroring the @uniqueItems comparability walk.
	if t.Named != nil {
		for _, arg := range t.Named.Args {
			a.mapKeysComparable(arg, f, typeParams)
		}
	}
}

// keyMarshalable reports whether key is a usable Go map key that
// encoding/json can also marshal/unmarshal: a string or integer kind, or a
// scalar / enum over one. Go also COMPILES a bool / float / all-comparable
// struct key, but json.Marshal returns "unsupported type" for those at
// runtime — so they are rejected even though they compile (the OpenAPI
// would advertise a serializable object the server can't produce). A
// qualified cross-package key is accepted conservatively (the project pass
// resolves it); a bare type-parameter is never a valid key.
func (a *analyzer) keyMarshalable(key *ast.TypeRef, typeParams []string) bool {
	if key == nil || key.Named == nil || key.Named.Name == nil || key.Array || key.Map != nil {
		return false
	}
	name := key.Named.Name.String()
	for _, tp := range typeParams {
		if tp == name {
			return false
		}
	}
	if len(key.Named.Name.Parts) > 1 {
		return true // cross-package ref; defer to the project resolver
	}
	if _, ok := a.pkg.Enums[name]; ok {
		return true // string- or int-backed enum
	}
	prim := name
	if sc, ok := a.pkg.Scalars[name]; ok {
		prim = sc.Primitive
	}
	switch prim {
	case "string",
		"int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64":
		return true
	}
	return false
}

// checkValueConstraintOnTypeParam rejects a field-level VALUE constraint
// (numeric or string) on a bare type-parameter field (`val T @gte(10)` in
// a generic decl). The validator is emitted once against the parametric
// receiver, where the element is `any`-constrained and so can't be
// compared / measured — yet the monomorphised OpenAPI advertises the
// bound, diverging from the (silently absent) runtime check. Array-shape
// constraints (@minItems / @maxItems) are NOT rejected here: they bound
// the slice length, which is knowable parametrically (@uniqueItems is
// handled separately by [checkUniqueItemsComparable]).
func (a *analyzer) checkValueConstraintOnTypeParam(f *ast.Field, typeParams []string) {
	if f == nil || f.Type == nil || f.Type.Array || f.Type.Map != nil || f.Type.Named == nil {
		return
	}
	name := f.Type.Named.Name.String()
	isParam := false
	for _, tp := range typeParams {
		if tp == name {
			isParam = true
			break
		}
	}
	if !isParam {
		return
	}
	for _, d := range f.Decorators {
		if d == nil {
			continue
		}
		switch d.Name {
		case "gte", "gt", "lte", "lt", "range", "positive", "negative", "multipleOf",
			"minLength", "maxLength", "length", "pattern", "format":
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
				"@%s cannot constrain a type-parameter field (%s): the parametric validator sees it as `any` and can't enforce the bound, while the monomorphised OpenAPI would still advertise it. Drop the decorator, or constrain a concrete type the instance supplies.",
				d.Name, name)
			return
		}
	}
}

// checkMultipleOfTarget rejects `@multipleOf` where the generated validator
// can't enforce what the OpenAPI advertises. Go's `%` operator is
// integer-only: a float field can't be checked at all, and an integer
// field with a fractional divisor (`@multipleOf(2.5)`) can't either — the
// validator silently drops it while the spec still advertises `multipleOf:
// 2.5`. Both are rejected so the spec and the validator agree.
func (a *analyzer) checkMultipleOfTarget(f *ast.Field) {
	if f == nil || f.Type == nil || f.Type.Named == nil {
		return
	}
	prim := f.Type.Named.Name.String()
	if sd, ok := a.pkg.Scalars[prim]; ok {
		prim = sd.Primitive
	}
	isFloat := prim == "float32" || prim == "float64"
	for _, d := range f.Decorators {
		if d == nil || d.Name != "multipleOf" {
			continue
		}
		if isFloat {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
				"@multipleOf does not support float fields — Go's modulus operator is integer-only. Move the field to an integer type or add a tolerance check in your handler.")
			continue
		}
		// Integer field: a fractional divisor is unenforceable by integer
		// modulus, yet the OpenAPI would advertise it — reject it.
		if len(d.Args) == 1 {
			if fl, ok := d.Args[0].Value.(*ast.FloatLit); ok && fl.Value != float64(int64(fl.Value)) {
				a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
					"@multipleOf on an integer field needs a whole-number divisor — Go's modulus is integer-only, so a fractional divisor can't be enforced (the OpenAPI would advertise a bound the validator drops). Use a whole number.")
			}
		}
	}
}

// checkPatternFormatOnBytes rejects `@pattern` / `@format` on a `bytes`
// field (or a bytes-backed scalar). Both decorators constrain TEXT — the
// validator emits a regexp / format check gated on a string shape, so a
// bytes field silently drops the check while the OpenAPI schema still
// advertises the pattern / format. A binary value has no string pattern;
// the author wants a `string` field.
func (a *analyzer) checkPatternFormatOnBytes(f *ast.Field) {
	if f == nil || f.Type == nil || f.Type.Array || f.Type.Map != nil || f.Type.Named == nil {
		return
	}
	prim := f.Type.Named.Name.String()
	if sd, ok := a.pkg.Scalars[prim]; ok {
		prim = sd.Primitive
	}
	if prim != "bytes" {
		return
	}
	for _, d := range f.Decorators {
		if d != nil && (d.Name == "pattern" || d.Name == "format") {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
				"@%s applies to text, not a `bytes` field — a binary value has no string pattern / format, so the runtime validator drops it while the OpenAPI schema would still advertise it. Use a `string` field, or drop the decorator.",
				d.Name)
		}
	}
}

// unsignedPrim reports whether a Go primitive name is an unsigned
// integer. `@negative` is contradictory on these (the value is always
// >= 0); `@positive` stays legal (it rejects only 0).
func unsignedPrim(prim string) bool {
	switch prim {
	case "uint", "uint8", "uint16", "uint32", "uint64":
		return true
	}
	return false
}

// integerPrim reports whether prim is a signed or unsigned integer
// primitive — the set whose @multipleOf is enforced with Go's modulus.
func integerPrim(prim string) bool {
	switch prim {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64":
		return true
	}
	return false
}

// checkNegativeOnUnsigned rejects `@negative` on an unsigned-integer
// field. The validator emits a `value >= 0` rejection, which fires for
// EVERY value of a `uint*` (always >= 0) — the field could never
// validate. Resolves through a named scalar so `count Quantity
// @negative` (scalar Quantity uint) is caught the same way a bare
// `count uint @negative` is.
func (a *analyzer) checkNegativeOnUnsigned(f *ast.Field) {
	if f == nil || f.Type == nil || f.Type.Named == nil {
		return
	}
	prim := f.Type.Named.Name.String()
	if sd, ok := a.pkg.Scalars[prim]; ok {
		prim = sd.Primitive
	}
	if !unsignedPrim(prim) {
		return
	}
	a.diagNegativeUnsigned(f.Decorators, prim)
	// `@lt(0)` is the desugared spelling of `@negative`: "value < 0", which
	// no `uint*` can satisfy. The capacity check ([checkBoundCapacity])
	// misses it because 0 is itself an in-range value — only the predicate
	// is empty. (`@lt(N)` / `@lte(N)` with N < 0 are already caught there
	// as out-of-range literals.)
	for _, d := range f.Decorators {
		if d != nil && d.Name == "lt" && len(d.Args) == 1 {
			if il, ok := d.Args[0].Value.(*ast.IntLit); ok && il.Value == 0 {
				a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
					"@lt(0) cannot apply to an unsigned type (%s is always >= 0) — every value would be rejected; use a signed integer or a positive bound", prim)
			}
		}
	}
}

// diagNegativeUnsigned emits the `@negative`-on-unsigned diagnostic for
// every `@negative` in decs. Shared by the field and scalar passes.
func (a *analyzer) diagNegativeUnsigned(decs []*ast.Decorator, prim string) {
	for _, d := range decs {
		if d != nil && d.Name == "negative" {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
				"@negative cannot apply to an unsigned type (%s is always >= 0) — every value would be rejected; use a signed integer or drop @negative", prim)
		}
	}
}

// checkUniqueItemsComparable rejects `@uniqueItems` on an array whose
// element type is NOT comparable (usable as a Go map key). The runtime
// dedupe loop builds `map[Elem]struct{}`, so a slice / map / `any` /
// `bytes` element — or a struct/generic transitively containing one —
// produces either non-compiling Go (`invalid map key type`) or a runtime
// `hash of unhashable type` panic, while the OpenAPI side still
// advertises `uniqueItems: true`. Catching it at design time keeps the
// generated validator compiling and the spec honest. Element types that
// can't be resolved in this package (cross-package qualified refs) are
// conservatively allowed to avoid false rejections.
func (a *analyzer) checkUniqueItemsComparable(f *ast.Field, typeParams []string) {
	if f == nil || f.Type == nil || !f.Type.Array {
		return
	}
	for _, d := range f.Decorators {
		if d == nil || d.Name != "uniqueItems" {
			continue
		}
		elem := peelOneArray(f.Type)
		// A type-parameter element (`items T[] @uniqueItems` in a generic
		// decl) is `any`-constrained on the parametric receiver, so the
		// dedupe `map[T]struct{}` cannot compile and the parametric
		// Validate() cannot enforce uniqueness — reject it like any other
		// incomparable element rather than emit non-compiling Go.
		if elem != nil && elem.Named != nil && elem.Named.Name != nil && !elem.Array && elem.Map == nil {
			name := elem.Named.Name.String()
			for _, tp := range typeParams {
				if tp == name {
					a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
						"@uniqueItems is not supported on a type-parameter element (%s): the parametric validator can't build a dedupe map over an `any`-constrained value. Drop @uniqueItems, or use a concrete comparable element type.", name)
					return
				}
			}
		}
		if !a.typeRefComparable(elem, map[string]bool{}) {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
				"@uniqueItems requires comparable elements (usable as a map key) — %s is not (a slice / map / `any`, or a struct/generic containing one). Restructure the element into a comparable shape, or drop @uniqueItems.",
				describeTypeRef(elem))
			return
		}
	}
}

// peelOneArray returns the element type after stripping ONE array
// dimension: `Tag[]` -> `Tag` (comparable scalar), `Tag[][]` -> `Tag[]`
// (still an array, hence non-comparable). Mirrors the codegen
// arrayElemType peel so the comparability verdict matches what the
// validator emits. Optional is cleared on the element.
func peelOneArray(t *ast.TypeRef) *ast.TypeRef {
	clone := *t
	clone.Optional = false
	if clone.ArrayDepth > 0 {
		clone.ArrayDepth--
	}
	if clone.ArrayDepth == 0 {
		clone.Array = false
	}
	return &clone
}

// typeRefComparable reports whether values of t are usable as a Go map
// key. Arrays / maps / `any` / `bytes` are not; a named struct or generic
// instance is comparable only when EVERY member is. `seen` guards against
// recursive types (a cycle is treated as comparable along the back-edge).
func (a *analyzer) typeRefComparable(t *ast.TypeRef, seen map[string]bool) bool {
	if t == nil {
		return false
	}
	if t.Array || t.Map != nil {
		return false
	}
	if t.Named == nil || t.Named.Name == nil {
		return false
	}
	name := t.Named.Name.String()
	switch name {
	case "any", "bytes", "file":
		return false
	case "string", "bool",
		"int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64":
		return true
	}
	if sc, ok := a.pkg.Scalars[name]; ok {
		return sc.Primitive != "bytes"
	}
	if _, ok := a.pkg.Enums[name]; ok {
		return true
	}
	if td, ok := a.pkg.Types[name]; ok {
		// Key the back-edge guard by the instantiated identity (name + args),
		// not the bare decl name — otherwise a comparable instantiation
		// (`Wrap<string>`) poisons the guard so a later non-comparable one
		// (`Wrap<bytes>`) short-circuits to "comparable" and leaks a
		// non-compiling dedupe map. A true cycle (same instantiation) still
		// matches and breaks.
		key := comparableKey(t)
		if seen[key] {
			return true
		}
		seen[key] = true
		// For a generic instance (`Pair<bytes>`) substitute the type-args
		// into the decl's fields: a field typed `T` is comparable only if the
		// concrete argument is. Without this, `T` resolves to nothing and
		// falls through to the "conservatively comparable" branch, so
		// `Pair<bytes>[] @uniqueItems` would pass the check and then emit a
		// non-compiling `map[Pair[[]byte]]`.
		subst := map[string]*ast.TypeRef{}
		if len(td.TypeParams) > 0 && t.Named != nil {
			for i, tp := range td.TypeParams {
				if i < len(t.Named.Args) {
					subst[tp] = t.Named.Args[i]
				}
			}
		}
		for _, m := range td.Body {
			switch v := m.(type) {
			case *ast.Field:
				if !a.typeRefComparable(substTypeParam(v.Type, subst), seen) {
					return false
				}
			case *ast.Mixin:
				if v.Ref != nil && v.Ref.Name != nil {
					// Substitute the outer decl's type-args into the mixin ref
					// too (a generic mixin `Inner<T>` becomes `Inner<bytes>`),
					// mirroring the Field branch above — without it a bare `T`
					// inside the mixin escapes the comparability check.
					if !a.typeRefComparable(substTypeParam(&ast.TypeRef{Named: v.Ref}, subst), seen) {
						return false
					}
				}
			}
		}
		return true
	}
	// Unresolved here (cross-package qualified ref or bare generic
	// type-param) — conservatively comparable to avoid a false reject.
	return true
}

// substTypeParam replaces a bare type-parameter reference (`T`) with its
// concrete argument from subst, and recurses into a nested generic
// instance's args (`Inner<T>`). Array / map fields are returned unchanged
// — they are non-comparable regardless of the element, so the caller
// rejects them before any substitution matters.
func substTypeParam(t *ast.TypeRef, subst map[string]*ast.TypeRef) *ast.TypeRef {
	if t == nil || t.Named == nil || t.Named.Name == nil {
		return t
	}
	if !t.Array && t.Map == nil {
		if rep, ok := subst[t.Named.Name.String()]; ok {
			return rep
		}
	}
	if len(t.Named.Args) > 0 {
		clone := *t
		nn := *t.Named
		nn.Args = make([]*ast.TypeRef, len(t.Named.Args))
		for i, arg := range t.Named.Args {
			nn.Args[i] = substTypeParam(arg, subst)
		}
		clone.Named = &nn
		return &clone
	}
	return t
}

// intCapacity returns the value range a Go integer primitive can hold.
// Returns ok=false for non-integer or unrecognised primitives so the
// caller skips the check rather than emit a false-positive overflow
// diagnostic.
func intCapacity(primitive string) (lo, hi float64, ok bool) {
	switch primitive {
	case "int8":
		return -128, 127, true
	case "int16":
		return -32768, 32767, true
	case "int32":
		return -2147483648, 2147483647, true
	case "int64", "int":
		// `int` is 32 or 64-bit depending on platform; treat as the
		// narrower of the two so designs stay portable.
		return -9223372036854775808, 9223372036854775807, true
	case "uint8":
		return 0, 255, true
	case "uint16":
		return 0, 65535, true
	case "uint32":
		return 0, 4294967295, true
	case "uint64", "uint":
		// `uint` follows the same portable-narrow rule as int.
		return 0, 18446744073709551615, true
	}
	return 0, 0, false
}

// checkBoundCapacity rejects numeric bound literals that exceed the
// field's primitive type capacity. Without this, codegen happily emits
// `if v.Small > 300 { ... }` against an `int8` field, which fails to
// compile because 300 overflows int8. Floats are skipped — Go float64
// captures every IntLit we accept, and float64 bounds for float32
// fields are tolerated.
func (a *analyzer) checkBoundCapacity(f *ast.Field) {
	if f == nil || f.Type == nil || f.Type.Array || f.Type.Named == nil {
		return
	}
	prim := f.Type.Named.Name.String()
	if sd, ok := a.pkg.Scalars[prim]; ok {
		prim = sd.Primitive
	}
	lo, hi, ok := intCapacity(prim)
	if !ok {
		return
	}
	check := func(d *ast.Decorator, arg *ast.DecoratorArg) {
		var v float64
		var disp string
		switch lit := arg.Value.(type) {
		case *ast.IntLit:
			v, disp = float64(lit.Value), strconv.FormatInt(lit.Value, 10)
		case *ast.FloatLit:
			// An INTEGRAL float bound (`@gte(300.0)`) renders to a bare
			// whole-number Go literal, so it must be capacity-checked too;
			// a fractional float is rejected separately by
			// checkIntBoundFloatLiteral.
			if lit.Value != float64(int64(lit.Value)) {
				return
			}
			v, disp = lit.Value, strconv.FormatInt(int64(lit.Value), 10)
		default:
			return
		}
		if v < lo || v > hi {
			a.diag(arg.Pos, arg.Pos, lexer.SeverityError, CodeBoundOverflow,
				"@%s bound %s exceeds %s range [%g, %g]",
				d.Name, disp, prim, lo, hi)
		}
	}
	for _, d := range f.Decorators {
		if d == nil {
			continue
		}
		// All single-arg comparison decorators + dual-arg @range.
		switch d.Name {
		case "gt", "gte", "lt", "lte", "multipleOf":
			if len(d.Args) == 1 {
				check(d, d.Args[0])
			}
		case "range":
			for _, ag := range d.Args {
				check(d, ag)
			}
		}
	}
}

// checkBoundLiteralKind rejects a fractional float bound on an
// integer-typed field. Resolves the field's primitive (following a
// local scalar to its underlying type) and defers to the shared
// per-decorator scan.
func (a *analyzer) checkBoundLiteralKind(f *ast.Field) {
	if f == nil || f.Type == nil || f.Type.Array || f.Type.Named == nil {
		return
	}
	prim := f.Type.Named.Name.String()
	if sd, ok := a.pkg.Scalars[prim]; ok {
		prim = sd.Primitive
	}
	a.checkIntBoundFloatLiteral(prim, fmt.Sprintf("field %q", f.Name), f.Decorators)
}

// checkIntBoundFloatLiteral rejects a fractional float bound literal
// (`@gte(0.5)`, `@range(0.5, 10.5)`, …) on an integer-typed target.
// codegen renders a comparison/range bound verbatim, so the literal
// `0.5` ends up compared against the integer field value — Go rejects
// that with "constant 0.5 truncated to integer" and the whole
// generated package fails to build. Catching it here turns an opaque
// downstream build error into a precise design-time diagnostic.
//
// Only genuinely fractional literals are rejected: an integral float
// (`1.0`, `1e3`) renders to a whole-number Go literal and compiles
// fine, and float-typed targets are skipped entirely because a
// fractional bound is exactly what they are for. `@multipleOf` is not
// included — its codegen takes the integer-only path and never emits a
// float literal.
func (a *analyzer) checkIntBoundFloatLiteral(prim, target string, decs []*ast.Decorator) {
	if _, _, ok := intCapacity(prim); !ok {
		return // not an integer primitive — float bounds are valid
	}
	for _, d := range decs {
		if d == nil {
			continue
		}
		var args []*ast.DecoratorArg
		switch d.Name {
		case "gt", "gte", "lt", "lte":
			if len(d.Args) == 1 {
				args = d.Args
			}
		case "range":
			args = d.Args
		default:
			continue
		}
		for _, ag := range args {
			fl, ok := fractionalArg(ag)
			if !ok {
				continue
			}
			a.diag(ag.Pos, ag.Pos, lexer.SeverityError, CodeDecoratorTypeMismatch,
				"@%s bound %g must be a whole number on integer %s — codegen compares the bound against an integer value, so a fractional literal would not compile",
				d.Name, fl.Value, target)
		}
	}
}

// fractionalArg reports a FloatLit argument whose value carries a
// fractional part. An integral float literal renders to a whole-number
// Go literal, so it is not flagged.
func fractionalArg(a *ast.DecoratorArg) (*ast.FloatLit, bool) {
	if a == nil {
		return nil, false
	}
	fl, ok := a.Value.(*ast.FloatLit)
	if !ok || fl.Value == math.Trunc(fl.Value) {
		return nil, false
	}
	return fl, true
}

// checkDecoratorRanges applies value-sanity checks to each decorator
// in the slice. The dispatch table is small and explicit so adding a
// new rule means adding one case here plus the helper.
func (a *analyzer) checkDecoratorRanges(decs []*ast.Decorator) {
	for _, d := range decs {
		if d == nil {
			continue
		}
		switch d.Name {
		case "length", "range":
			a.checkPairArgs(d)
		case "multipleOf":
			a.checkMultipleOf(d)
		case "status":
			a.checkHTTPStatus(d)
		case "timeout":
			a.checkPositiveDuration(d)
		case "maxBodySize", "maxSize":
			a.checkPositiveSize(d)
		case "minLength", "maxLength", "minItems", "maxItems":
			a.checkNonNegativeInt(d)
		}
	}
}

// checkPairArgs handles `@length(min, max)` / `@range(min, max)`. The
// 1-arg form of `@length` is "exact length" — still non-negative, or the
// validator emits an always-true `l != N` reject (RuneCount is never < 0)
// while OpenAPI advertises no length constraint at all.
func (a *analyzer) checkPairArgs(d *ast.Decorator) {
	pos := positionalArgs(d)
	if d.Name == "length" && len(pos) == 1 {
		if v, ok := numericValue(pos[0].Value); ok && v < 0 {
			a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
				"@length: exact length must be ≥ 0 (got %g)", v)
		}
		return
	}
	if len(pos) != 2 {
		return
	}
	lo, loOk := numericValue(pos[0].Value)
	hi, hiOk := numericValue(pos[1].Value)
	if !loOk || !hiOk {
		return
	}
	if lo > hi {
		a.diag(pos[1].Pos, pos[1].Pos, lexer.SeverityError, CodeDecoratorRange,
			"@%s: min (%g) must be ≤ max (%g)", d.Name, lo, hi)
	}
	if d.Name == "length" && lo < 0 {
		a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
			"@length: min must be ≥ 0 (got %g)", lo)
	}
}

// checkMultipleOf rejects non-positive divisors. Zero panics at
// runtime (division by zero). Negative divisors are mathematically
// valid in Go (`%` follows the dividend sign) but every common
// interpretation of "multiple of N" means N > 0 — accepting negatives
// silently lets a typo (`@multipleOf(-2)`) compile to a validator
// that exhibits surprising symmetry around the dividend's sign.
func (a *analyzer) checkMultipleOf(d *ast.Decorator) {
	pos := positionalArgs(d)
	if len(pos) != 1 {
		return
	}
	v, ok := numericValue(pos[0].Value)
	if !ok {
		return
	}
	if v == 0 {
		a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
			"@multipleOf: divisor must not be 0")
		return
	}
	if v < 0 {
		a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
			"@multipleOf: divisor must be positive (got %g)", v)
	}
}

// checkHTTPStatus rejects `@status(code)` outside the 100..599 range.
// Tightening to a known-status set is intentionally avoided - RFC
// allows future additions and we don't want to lag the spec.
func (a *analyzer) checkHTTPStatus(d *ast.Decorator) {
	pos := positionalArgs(d)
	if len(pos) != 1 {
		return
	}
	v, ok := pos[0].Value.(*ast.IntLit)
	if !ok {
		return
	}
	if v.Value < 100 || v.Value > 599 {
		a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
			"@status: HTTP status code must be in 100..599 (got %d)", v.Value)
	}
}

// checkPositiveDuration rejects `@timeout(0)` etc. Bare-int form
// (interpreted as seconds) is also checked. Negative values are
// likewise rejected.
func (a *analyzer) checkPositiveDuration(d *ast.Decorator) {
	pos := positionalArgs(d)
	if len(pos) != 1 {
		return
	}
	if v, ok := pos[0].Value.(*ast.IntLit); ok && v.Value <= 0 {
		a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
			"@%s: duration must be > 0 (got %d)", d.Name, v.Value)
	}
}

// checkPositiveSize rejects `@maxBodySize(0)` - accepts any request
// silently. Negative sizes are nonsensical.
func (a *analyzer) checkPositiveSize(d *ast.Decorator) {
	pos := positionalArgs(d)
	if len(pos) != 1 {
		return
	}
	if v, ok := pos[0].Value.(*ast.IntLit); ok && v.Value <= 0 {
		a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
			"@%s: size must be > 0 (got %d)", d.Name, v.Value)
	}
}

// checkNonNegativeInt rejects negative @minLength etc. Length / item
// counts cannot be negative; if the user wrote -1 they likely meant 0.
func (a *analyzer) checkNonNegativeInt(d *ast.Decorator) {
	pos := positionalArgs(d)
	if len(pos) != 1 {
		return
	}
	if v, ok := pos[0].Value.(*ast.IntLit); ok && v.Value < 0 {
		a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRange,
			"@%s: value must be ≥ 0 (got %d)", d.Name, v.Value)
	}
}

// checkPairOrdering enforces "lower decorator ≤ upper decorator" when
// both appear on the same field. Missing one of the pair is fine — the
// solo decorator is unconstrained. Four pair families:
//
//   - String length: `@minLength` vs `@maxLength`
//   - Array items:   `@minItems` vs `@maxItems`
//   - Numeric (inclusive): `@gte` vs `@lte`
//   - Numeric (strict):    `@gt`  vs `@lt`
//
// Mixed strict/inclusive pairs (`@gte(5) @lt(5)` etc.) are inspected
// for emptiness — when at least one bound is strict and the endpoints
// touch, no value satisfies both checks. Without this, codegen happily
// emits a validator that rejects every input.
func (a *analyzer) checkPairOrdering(f *ast.Field) {
	pairs := []struct {
		lo, hi   string
		loStrict bool
		hiStrict bool
	}{
		{lo: "minLength", hi: "maxLength"},
		{lo: "minItems", hi: "maxItems"},
		{lo: "gte", hi: "lte"},
		{lo: "gt", hi: "lt", loStrict: true, hiStrict: true},
		{lo: "gte", hi: "lt", hiStrict: true},
		{lo: "gt", hi: "lte", loStrict: true},
	}
	for _, p := range pairs {
		loV, loPos, loOk := singleNumericArg(f.Decorators, p.lo)
		hiV, hiPos, hiOk := singleNumericArg(f.Decorators, p.hi)
		if !loOk || !hiOk {
			continue
		}
		if loV > hiV {
			diag := a.diag(hiPos, hiPos, lexer.SeverityError, CodeDecoratorRange,
				"@%s (%g) must be ≥ @%s (%g)", p.hi, hiV, p.lo, loV)
			diag.Related = related(loPos, "@"+p.lo+" declared here")
			continue
		}
		// Equal endpoints define an empty value set whenever EITHER
		// bound is strict — `@gt(5) @lte(5)` excludes 5 on the
		// lower side, `@gte(5) @lt(5)` excludes 5 on the upper side,
		// `@gt(5) @lt(5)` excludes 5 on both. Only fully-inclusive
		// `@gte(N) @lte(N)` accepts the single value N.
		if loV == hiV && (p.loStrict || p.hiStrict) {
			diag := a.diag(hiPos, hiPos, lexer.SeverityWarning, CodeBoundEmptyRange,
				"@%s(%g) combined with @%s(%g) defines an empty range — no value satisfies both",
				p.hi, hiV, p.lo, loV)
			diag.Related = related(loPos, "@"+p.lo+" declared here")
		}
	}
}

// checkNullableRedundant warns when `@nullable` is applied to a `T?`
// field. README §"Field presence semantics" splits the four states
// explicitly; the optional marker already conveys nullability so the
// decorator is noise.
func (a *analyzer) checkNullableRedundant(f *ast.Field) {
	var nullableDec *ast.Decorator
	for _, d := range f.Decorators {
		if d == nil {
			continue
		}
		if d.Name == "nullable" {
			nullableDec = d
		}
	}
	if nullableDec != nil && f.Type != nil && f.Type.Optional {
		a.diag(nullableDec.Pos, decoratorEnd(nullableDec),
			lexer.SeverityWarning, CodeDecoratorRedundant,
			"@nullable is redundant on optional field %q (the `?` already allows null)",
			f.Name)
	}
}

// singleNumericArg looks up the first decorator named `name` in decs
// and extracts its first numeric positional argument. Returns the
// numeric value, the position the IDE should underline, and ok=false
// when the decorator is absent or its first arg isn't numeric.
func singleNumericArg(decs []*ast.Decorator, name string) (float64, lexer.Position, bool) {
	for _, d := range decs {
		if d == nil || d.Name != name {
			continue
		}
		pos := positionalArgs(d)
		if len(pos) == 0 {
			return 0, lexer.Position{}, false
		}
		v, ok := numericValue(pos[0].Value)
		if !ok {
			return 0, lexer.Position{}, false
		}
		return v, pos[0].Pos, true
	}
	return 0, lexer.Position{}, false
}

// numericValue extracts a float64 from an int or float literal. Other
// expr kinds return ok=false.
func numericValue(e ast.Expr) (float64, bool) {
	switch v := e.(type) {
	case *ast.IntLit:
		return float64(v.Value), true
	case *ast.FloatLit:
		return v.Value, true
	}
	return 0, false
}
