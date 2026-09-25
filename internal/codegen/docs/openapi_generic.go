package docs

import (
	"slices"
	"strings"
	"unicode"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// genericRegistry turns each distinct generic instance into one component; a
// recursive one ends in a $ref to itself (`Tree<User>`'s `kids Tree<T>[]`).
type genericRegistry struct {
	// instances maps a component name (`PageOfUser`) to the instance that
	// first registered it.
	instances map[string]*genericInstance
	// emitted holds the names whose component is in the document.
	emitted map[string]bool
	// order is the registration order.
	order []string
	// dups holds the names two structurally distinct instances share:
	// `Page<int[]>` and `Page<IntArray>` are both `PageOfIntArray`.
	dups map[string]bool
	// resolver resolves the document package's type names.
	resolver *semantic.Resolver
}

// genericInstance is one generic declaration with its arguments, both in the
// merged package's names.
type genericInstance struct {
	decl *ast.TypeDecl
	args []*ast.TypeRef
	name string
}

// newGenericRegistry returns an empty registry.
func newGenericRegistry() *genericRegistry {
	return &genericRegistry{
		instances: map[string]*genericInstance{},
		emitted:   map[string]bool{},
		dups:      map[string]bool{},
	}
}

// refName returns the component n refs: its generic instance, registered on
// first use (`Page<Item>` → `PageOfItem`), else n's own name.
func (r *genericRegistry) refName(n *ast.NamedTypeRef) string {
	name := n.Name.String()
	if len(n.Args) > 0 {
		if decl := r.resolver.LookupType(name); decl != nil && len(decl.TypeParams) > 0 {
			return r.register(decl, n.Args)
		}
	}
	return name
}

// register records the instance of decl over args and returns its component
// name; a different instance under a name already taken goes to dups.
func (r *genericRegistry) register(decl *ast.TypeDecl, args []*ast.TypeRef) string {
	name := genericComponentName(decl, args)
	if existing, ok := r.instances[name]; ok {
		if existing.decl != decl || !slices.EqualFunc(existing.args, args, (*ast.TypeRef).Equal) {
			r.dups[name] = true
		}
		return name
	}
	r.instances[name] = &genericInstance{decl: decl, args: args, name: name}
	r.order = append(r.order, name)
	return name
}

// pending returns the unemitted instances, in registration order.
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

// markEmitted takes name out of [genericRegistry.pending].
func (r *genericRegistry) markEmitted(name string) {
	r.emitted[name] = true
}

// genericComponentName names an instance `<Decl>Of<Arg>And<Arg>...`, never
// truncated:
//
//	Page<User>                 → PageOfUser
//	Page<User<Test>>           → PageOfUserOfTest
//	Result<User, Error>        → ResultOfUserAndError
//	Page<string>               → PageOfString
//	Page<Order[]>              → PageOfOrderArray
//	Page<map<string, Order[]>> → PageOfMapOfStringAndOrderArray
//	Page<map<string, Order>[]> → PageOfArrayOfMapOfStringAndOrder
func genericComponentName(decl *ast.TypeDecl, args []*ast.TypeRef) string {
	return pascalIdent(decl.Name) + "Of" + argsName(args)
}

// argsName joins the name fragments of args with `And`.
func argsName(args []*ast.TypeRef) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = typeRefName(a)
	}
	return strings.Join(parts, "And")
}

// typeRefName names one type argument: `[]` and `?` end a leaf's name as `Array`
// and `OrNull`, and lead a compound's as `ArrayOf` and `NullOr`.
func typeRefName(t *ast.TypeRef) string {
	switch {
	case t == nil:
		return "Unknown"
	case t.Optional:
		present := *t
		present.Optional = false
		if compound(&present) {
			return "NullOr" + typeRefName(&present)
		}
		return typeRefName(&present) + "OrNull"
	case t.Array:
		elem := t.ElemTypeRef()
		if compound(elem) {
			return "ArrayOf" + typeRefName(elem)
		}
		return typeRefName(elem) + "Array"
	case t.Map != nil:
		return "MapOf" + typeRefName(t.Map.Key) + "And" + typeRefName(t.Map.Value)
	case t.Named != nil:
		return namedTypeName(t.Named)
	}
	return "Unknown"
}

// compound reports whether t is a map or a generic instance, or an array of one.
func compound(t *ast.TypeRef) bool {
	for t != nil && t.Array {
		t = t.ElemTypeRef()
	}
	return t != nil && (t.Map != nil || t.Named != nil && len(t.Named.Args) > 0)
}

// namedTypeName returns the name fragment of a named argument, its own
// arguments included (`User<Test>` → `UserOfTest`).
func namedTypeName(n *ast.NamedTypeRef) string {
	if n == nil {
		return "Unknown"
	}
	name := pascalIdent(n.Name.String())
	if len(n.Args) == 0 {
		return name
	}
	return name + "Of" + argsName(n.Args)
}

// pascalIdent upper-cases the first rune of name.
func pascalIdent(name string) string {
	if name == "" {
		return ""
	}
	runes := []rune(name)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}
