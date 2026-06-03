package codegen

import (
	"strings"
	"unicode"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// genericRegistry collects every distinct generic instantiation tuple
// encountered during OpenAPI emission so each one becomes a reusable
// component instead of being inlined at every reference site. Without
// reuse, an API that returns `Page<User>` from ten endpoints would
// duplicate the same Page body ten times in the spec - clients then
// emit ten incompatible TS types instead of a single shared one.
//
// The registry is also the termination mechanism for recursive
// generics: `type Tree<T> = { val: T, kids: Tree<T>[] }` instantiated
// with `Tree<User>` registers the instance, starts emitting its body,
// hits the `kids: Tree<T>[]` field, substitutes T=User to recover
// `Tree<User>` again, asks the registry for the component name, sees
// it is already registered, and returns a `$ref` to itself - the
// cycle terminates as a reference instead of an infinite inline loop.
type genericRegistry struct {
	// instances maps the synthetic component name (e.g.
	// `PageOfUser`) to the (decl, args) tuple that produced it.
	// Re-registering the same tuple is a no-op; the first registration
	// wins so iteration order stays deterministic.
	instances map[string]*genericInstance
	// emitted tracks names whose component body has been built and
	// placed into `doc.Components.Schemas`. A name in `instances` but
	// not yet `emitted` is still pending.
	emitted map[string]bool
	// order preserves registration order so emission output is stable
	// across runs (maps iterate randomly in Go).
	order []string
	// dups collects synthetic component names that TWO structurally
	// distinct instances resolve to — e.g. `Page<int[]>` and
	// `Page<IntArray>` both name-collapse to `PageOfIntArray`. Without
	// this they silently share one schema and one field is advertised with
	// the wrong shape; the generator rejects up front instead.
	dups map[string]bool
}

// genericInstance is the descriptor stored in [genericRegistry] for one
// unique generic instantiation. The decl pointer references the
// merge-renamed TypeDecl that owns the generic; args is the post-merge
// arg list as supplied at the reference site. Both have already been
// run through `rewriteTypeRef` when the project goes through the
// multi-package merge, so component-name computation only deals with
// names that are valid in the synthetic merged package.
type genericInstance struct {
	decl *ast.TypeDecl
	args []*ast.TypeRef
	name string
}

// newGenericRegistry returns an empty registry. Callers (chiefly
// [GenerateOpenAPI]) hold exactly one per document being built; the
// registry is passed down through every schema-emission function so
// nested generic encounters all funnel into the same set.
func newGenericRegistry() *genericRegistry {
	return &genericRegistry{
		instances: map[string]*genericInstance{},
		emitted:   map[string]bool{},
		dups:      map[string]bool{},
	}
}

// register adds the (decl, args) tuple to the registry if not already
// present, and returns the synthetic component name the caller should
// `$ref`. The same tuple registered twice yields the same name without
// allocating a duplicate entry.
func (r *genericRegistry) register(decl *ast.TypeDecl, args []*ast.TypeRef) string {
	name := genericComponentName(decl, args)
	if existing, ok := r.instances[name]; ok {
		// Same synthetic name, but if the args are structurally different
		// (e.g. an array arg int[] vs a struct named IntArray both yield the
		// "IntArray" fragment) the two instances are NOT the same schema —
		// record the collision rather than silently aliasing them.
		if existing.decl != decl || !typeRefsEqual(existing.args, args) {
			r.dups[name] = true
		}
		return name
	}
	r.instances[name] = &genericInstance{decl: decl, args: args, name: name}
	r.order = append(r.order, name)
	return name
}

// typeRefsEqual reports whether two generic-arg lists are structurally
// identical (so they denote the same instantiation).
func typeRefsEqual(a, b []*ast.TypeRef) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Equal(b[i]) {
			return false
		}
	}
	return true
}

// pending returns instances whose body has not been emitted yet, in
// registration order. The emitter loops until pending is empty;
// emitting one instance may register new (deeper) ones, so the loop
// rechecks after each pass.
func (r *genericRegistry) pending() []*genericInstance {
	var out []*genericInstance
	for _, name := range r.order {
		if r.emitted[name] {
			continue
		}
		out = append(out, r.instances[name])
	}
	return out
}

// markEmitted records that the instance's component schema has been
// placed in the document; subsequent [pending] calls skip it.
func (r *genericRegistry) markEmitted(name string) {
	r.emitted[name] = true
}

// genericComponentName computes the synthetic component schema name for
// one generic instantiation using FastAPI's `BaseOfArg1AndArg2...`
// convention. The base name is the decl's PascalCase identifier (which
// has already been collision-prefixed by mergeProjectForOpenAPI when
// two packages declared the same generic); arg names join with `Of`
// for the first arg and `And` for subsequent ones.
//
// Examples:
//
//	Page<User>            → PageOfUser
//	Page<User<Test>>      → PageOfUserOfTest
//	Result<User, Error>   → ResultOfUserAndError
//	Page<users.User>      → PageOfUsersUser   (cross-pkg arg)
//	Page<string>          → PageOfString      (primitive arg)
//	Page<Order[]>         → PageOfOrderArray  (array of named)
//	Page<Order?>          → PageOfOrderOrNull (optional named)
//
// Deep nesting produces a long but fully readable name; the name is
// never truncated or hashed, so it stays stable and tooling-friendly.
func genericComponentName(decl *ast.TypeDecl, args []*ast.TypeRef) string {
	var b strings.Builder
	b.WriteString(pascalIdent(decl.Name))
	b.WriteString("Of")
	for i, a := range args {
		if i > 0 {
			b.WriteString("And")
		}
		b.WriteString(typeRefName(a))
	}
	return b.String()
}

// typeRefName returns the name fragment for one type argument inside a
// generic instantiation. The fragment is concatenated into the parent
// component name; for nested generics it recurses through this same
// function so `Page<User<Test>>` produces `PageOfUserOfTest` and
// `Map<string, Page<User>>` produces `MapOfStringAndPageOfUser`.
//
// `?` (optional) and `[]` (array) wrappers on the arg both translate
// into name suffixes (`OrNull`, `Array`) so an arg that differs only
// by wrapper still produces a distinct, collision-free name. The
// alternative - dropping wrappers from the name - would collapse
// `Page<User>` and `Page<User?>` to the same component, which is wrong
// because the two schemas have different `nullable` behaviour.
func typeRefName(t *ast.TypeRef) string {
	if t == nil {
		return "Unknown"
	}
	var name string
	switch {
	case t.Map != nil:
		name = "MapOf" + typeRefName(t.Map.Key) + "And" + typeRefName(t.Map.Value)
	case t.Array:
		inner := *t
		inner.Array = false
		if inner.ArrayDepth > 0 {
			inner.ArrayDepth--
			inner.Array = inner.ArrayDepth > 0
		}
		// Optional only applies to the outer wrapper, not the element.
		inner.Optional = false
		name = typeRefName(&inner) + "Array"
	case t.Named != nil:
		name = namedTypeName(t.Named)
	default:
		name = "Unknown"
	}
	if t.Optional {
		name += "OrNull"
	}
	return name
}

// namedTypeName returns the name fragment for a NamedTypeRef. For a
// primitive (`string`, `int`, ...) it returns the Pascal-cased form
// (`String`, `Int`). For a non-primitive ident it prefixes the package
// segment when present so `users.User` becomes `UsersUser` - that
// matches the cross-package naming convention used elsewhere in
// `mergeProjectForOpenAPI`. Generic args on the named ref recurse
// through [typeRefName] so `Page<User<Test>>` ends up as
// `PageOfUserOfTest`.
func namedTypeName(n *ast.NamedTypeRef) string {
	if n == nil {
		return "Unknown"
	}
	bare := n.Name.String()
	if isPrimitiveName(bare) {
		return pascalIdent(bare)
	}
	// Cross-pkg qualified ref: join segments with PascalCase concat.
	full := pascalQualified(bare)
	if len(n.Args) == 0 {
		return full
	}
	var b strings.Builder
	b.WriteString(full)
	b.WriteString("Of")
	for i, a := range n.Args {
		if i > 0 {
			b.WriteString("And")
		}
		b.WriteString(typeRefName(a))
	}
	return b.String()
}

// isPrimitiveName reports whether name is one of the DSL primitive
// types whose schema is inlined, not referenced. Primitives still need
// a name fragment for generic component naming (`Page<string>` →
// `PageOfString`) but they do not produce a separate component schema.
// Delegates to [idents.IsBuiltin] so the codegen and the rest of the
// pipeline share one source of truth for "is this a DSL builtin".
func isPrimitiveName(name string) bool {
	if !idents.IsBuiltin(name) {
		return false
	}
	// `object` is the example-only bag type and never reaches the
	// OpenAPI schema emitter as a field; exclude it so callers don't
	// accidentally classify a struct field as a primitive.
	return name != "object"
}

// pascalIdent upper-cases the first rune of name. Used for simple
// single-segment identifiers; multi-segment (`users.User`) goes
// through [pascalQualified] instead.
func pascalIdent(name string) string {
	if name == "" {
		return ""
	}
	runes := []rune(name)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// pascalQualified turns a possibly dot-qualified DSL name into its
// flat PascalCase form. `users.User` → `UsersUser`, `User` → `User`.
// Used both for the generic decl's own name (when it lives in a
// cross-pkg position) and for named arg references inside an
// instantiation.
func pascalQualified(name string) string {
	if !strings.Contains(name, ".") {
		return pascalIdent(name)
	}
	parts := strings.Split(name, ".")
	for i, p := range parts {
		parts[i] = pascalIdent(p)
	}
	return strings.Join(parts, "")
}

// collectGenericInstancesInPackage walks every TypeDecl, ErrorDecl
// and ServiceDecl in pkg and registers every generic instantiation it
// finds. The pre-pass is what guarantees the registry has captured
// every needed component before emission begins; new instances
// introduced during emission (from nested generic bodies, mixin
// expansion, ...) still register through the same registry but the
// pre-pass means the simplest cases are already deduplicated and
// available to schemaForTypeRef during the main schema emission.
func collectGenericInstancesInPackage(pkg *semantic.Package, registry *genericRegistry) {
	if pkg == nil || registry == nil {
		return
	}
	visit := func(t *ast.TypeRef) {
		walkTypeRefForGenerics(t, pkg, registry)
	}
	for _, td := range pkg.Types {
		if len(td.TypeParams) > 0 {
			continue
		}
		for _, m := range td.Body {
			if f, ok := m.(*ast.Field); ok {
				visit(f.Type)
			}
		}
	}
	for _, ed := range pkg.Errors {
		for _, m := range ed.Body {
			if f, ok := m.(*ast.Field); ok {
				visit(f.Type)
			}
		}
	}
	for _, si := range pkg.Services {
		for _, m := range si.Methods {
			if m.Request != nil {
				// Method.Request is a NamedTypeRef pointer; build a
				// synthetic TypeRef so the walker sees it uniformly.
				visit(&ast.TypeRef{Named: m.Request})
			}
			if m.Response != nil && m.Response.Type != nil {
				visit(&ast.TypeRef{Named: m.Response.Type})
			}
		}
	}
}

// walkTypeRefForGenerics descends through arrays / maps / named-with-
// args and registers any generic instantiations encountered. Primitive
// and plain-named refs are skipped - they do not produce synthetic
// components.
func walkTypeRefForGenerics(t *ast.TypeRef, pkg *semantic.Package, registry *genericRegistry) {
	if t == nil {
		return
	}
	if t.Map != nil {
		walkTypeRefForGenerics(t.Map.Key, pkg, registry)
		walkTypeRefForGenerics(t.Map.Value, pkg, registry)
		return
	}
	if t.Array {
		inner := *t
		inner.Array = false
		if inner.ArrayDepth > 0 {
			inner.ArrayDepth--
			inner.Array = inner.ArrayDepth > 0
		}
		inner.Optional = false
		walkTypeRefForGenerics(&inner, pkg, registry)
		return
	}
	if t.Named == nil || len(t.Named.Args) == 0 {
		return
	}
	for _, a := range t.Named.Args {
		walkTypeRefForGenerics(a, pkg, registry)
	}
	if decl, ok := pkg.Types[t.Named.Name.String()]; ok && len(decl.TypeParams) > 0 {
		registry.register(decl, t.Named.Args)
	}
}

// substMap pairs a generic decl's type parameters with the concrete
// arguments of one instantiation (`T` → `Item`). Extra params beyond the
// supplied args are left unmapped. The OpenAPI schema instantiation, the
// response-field substitution, and the mixin field flatten all build this
// same map, so they share one definition.
func substMap(typeParams []string, args []*ast.TypeRef) map[string]*ast.TypeRef {
	subst := make(map[string]*ast.TypeRef, len(typeParams))
	for i, p := range typeParams {
		if i < len(args) {
			subst[p] = args[i]
		}
	}
	return subst
}
