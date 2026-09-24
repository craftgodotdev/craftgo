package docs

import (
	"strings"
	"unicode"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/prims"
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
	// resolver resolves type names for the field IR.
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

// register records the instance of decl over args and returns its component
// name; a different instance under a name already taken goes to dups.
func (r *genericRegistry) register(decl *ast.TypeDecl, args []*ast.TypeRef) string {
	name := genericComponentName(decl, args)
	if existing, ok := r.instances[name]; ok {
		if existing.decl != decl || !typeRefsEqual(existing.args, args) {
			r.dups[name] = true
		}
		return name
	}
	r.instances[name] = &genericInstance{decl: decl, args: args, name: name}
	r.order = append(r.order, name)
	return name
}

// typeRefsEqual reports whether two argument lists are structurally equal.
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
//	Page<User>           → PageOfUser
//	Page<User<Test>>     → PageOfUserOfTest
//	Result<User, Error>  → ResultOfUserAndError
//	Page<string>         → PageOfString
//	Page<Order[]>        → PageOfOrderArray
//	Page<Order?>         → PageOfOrderOrNull
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

// typeRefName returns the name fragment of one type argument; `[]` and `?`
// add `Array` and `OrNull`, so `Page<User>` and `Page<User?>` differ.
func typeRefName(t *ast.TypeRef) string {
	if t == nil {
		return "Unknown"
	}
	var name string
	switch {
	case t.Map != nil:
		name = "MapOf" + typeRefName(t.Map.Key) + "And" + typeRefName(t.Map.Value)
	case t.Array:
		inner := t.ElemTypeRef()
		name = typeRefName(inner) + "Array"
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

// namedTypeName returns the name fragment of a named argument, its own
// arguments included (`User<Test>` → `UserOfTest`).
func namedTypeName(n *ast.NamedTypeRef) string {
	if n == nil {
		return "Unknown"
	}
	bare := n.Name.String()
	if isPrimitiveName(bare) {
		return pascalIdent(bare)
	}
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

// isPrimitiveName reports whether name is a DSL primitive other than
// `object`, the example-only type.
func isPrimitiveName(name string) bool {
	if !prims.Is(name) {
		return false
	}
	return name != "object"
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

// pascalQualified joins the segments of a dotted name, each upper-cased
// first (`users.User` → `UsersUser`).
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

// collectGenericInstancesInPackage registers every generic instance named by
// pkg's type and error fields and by its method requests and responses.
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
				visit(&ast.TypeRef{Named: m.Request})
			}
			if m.Response != nil && m.Response.Type != nil {
				visit(&ast.TypeRef{Named: m.Response.Type})
			}
		}
	}
}

// walkTypeRefForGenerics registers every generic instance in t, arguments,
// array elements and map entries included.
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
		inner := t.ElemTypeRef()
		walkTypeRefForGenerics(inner, pkg, registry)
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
