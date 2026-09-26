package docs

import (
	"slices"
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// mergedKinds are the declarations the merged document names: types, enums,
// scalars and errors.
const mergedKinds = semantic.TypeRefDecls | semantic.ErrorDecls

// mergedNames returns the name the merged document gives each declaration of
// mergedKinds: its own, or `<PascalPkg><Name>` when two packages declare it.
func mergedNames(proj *semantic.Project) map[ast.Decl]string {
	declared := map[string]int{}
	for _, pkgName := range proj.PackageNames() {
		for _, d := range proj.Packages[pkgName].Decls(mergedKinds) {
			declared[d.DeclName()]++
		}
	}
	names := map[ast.Decl]string{}
	for _, pkgName := range proj.PackageNames() {
		for _, d := range proj.Packages[pkgName].Decls(mergedKinds) {
			name := d.DeclName()
			if declared[name] >= 2 {
				name = idents.PascalCase(pkgName) + name
			}
			names[d] = name
		}
	}
	return names
}

// projectMergeCollisions returns the merged names two declarations share,
// such as `shared.User` renamed to the `SharedUser` that package api declares.
func projectMergeCollisions(proj *semantic.Project) []string {
	owners := map[string]int{}
	for _, name := range mergedNames(proj) {
		owners[name]++
	}
	var dups []string
	for name, n := range owners {
		if n >= 2 {
			dups = append(dups, name)
		}
	}
	slices.Sort(dups)
	return dups
}

// merger copies a project's declarations into one package, each reference
// renamed to the merged name of the declaration semantic resolves it to.
type merger struct {
	proj  *semantic.Project
	names map[ast.Decl]string
	taken map[string]bool // every merged name
}

// mergeProjectForOpenAPI returns proj's packages as one package, each
// declaration under its merged name.
func mergeProjectForOpenAPI(proj *semantic.Project) *semantic.Package {
	m := &merger{proj: proj, names: mergedNames(proj), taken: map[string]bool{}}
	for _, name := range m.names {
		m.taken[name] = true
	}
	out := &semantic.Package{
		Types:    map[string]*ast.TypeDecl{},
		Enums:    map[string]*ast.EnumDecl{},
		Errors:   map[string]*ast.ErrorDecl{},
		Scalars:  map[string]*ast.ScalarDecl{},
		Services: map[string]*semantic.ServiceInfo{},
	}
	pkgNames := proj.PackageNames()
	if len(pkgNames) > 0 {
		out.Name = pkgNames[0]
	}
	for _, pkgName := range pkgNames {
		p := proj.Packages[pkgName]
		for _, d := range p.Decls(mergedKinds) {
			switch d := d.(type) {
			case *ast.TypeDecl:
				cp := *d
				cp.Name = m.names[d]
				var params map[string]string
				cp.TypeParams, params = m.typeParams(d.TypeParams)
				cp.Body = m.members(pkgName, d.Body, params)
				out.Types[cp.Name] = &cp
			case *ast.EnumDecl:
				cp := *d
				cp.Name = m.names[d]
				out.Enums[cp.Name] = &cp
			case *ast.ScalarDecl:
				cp := *d
				cp.Name = m.names[d]
				out.Scalars[cp.Name] = &cp
			case *ast.ErrorDecl:
				cp := *d
				cp.Name = m.names[d]
				cp.Body = m.members(pkgName, d.Body, nil)
				out.Errors[cp.Name] = &cp
			}
		}
		// A service without methods is left out.
		for name, si := range p.Services {
			if len(si.Methods) == 0 {
				continue
			}
			out.Services[serviceKey(pkgName, name)] = m.service(pkgName, si)
		}
	}
	return out
}

// serviceKey is the merged package's key of package pkg's service name: its
// qualified name, `a.S`, so two packages' services of one name both stay.
func serviceKey(pkg, name string) string { return pkg + "." + name }

// servicePackage returns the package of the merged service under key.
func servicePackage(key string) string {
	pkg, _, _ := strings.Cut(key, ".")
	return pkg
}

// typeParams returns params as the merged package names them, and each
// parameter to that name: its own, or, when a merged declaration takes it,
// the first free one with a number appended (`ADup` → `ADup2`).
func (m *merger) typeParams(params []string) ([]string, map[string]string) {
	if len(params) == 0 {
		return params, nil
	}
	used := map[string]bool{}
	for _, p := range params {
		used[p] = true
	}
	names := make([]string, len(params))
	scope := make(map[string]string, len(params))
	for i, p := range params {
		name := p
		for n := 2; m.taken[name] || (name != p && used[name]); n++ {
			name = p + strconv.Itoa(n)
		}
		used[name] = true
		names[i], scope[p] = name, name
	}
	return names, scope
}

// ref returns n, written in package home with the type parameters params
// maps in scope, under the merged name of the parameter or of the
// declaration of kinds it resolves to, type arguments too.
func (m *merger) ref(home string, n *ast.NamedTypeRef, params map[string]string, kinds semantic.DeclKind) *ast.NamedTypeRef {
	if n == nil || n.Name == nil {
		return n
	}
	out := n
	name := n.Name.String()
	final, ok := params[name]
	if !ok && !prims.Is(name) {
		final, ok = m.names[m.proj.Lookup(home, name, kinds)]
	}
	if ok && final != name {
		cp := *n
		cp.Name = &ast.QualifiedIdent{Pos: n.Name.Pos, Parts: []string{final}}
		out = &cp
	}
	if len(n.Args) > 0 {
		if out == n {
			cp := *n
			out = &cp
		}
		out.Args = make([]*ast.TypeRef, len(n.Args))
		for i, a := range n.Args {
			out.Args[i] = m.typeRef(home, a, params)
		}
	}
	return out
}

// typeRef returns a copy of t with every named ref in it renamed by
// [merger.ref], map entries included.
func (m *merger) typeRef(home string, t *ast.TypeRef, params map[string]string) *ast.TypeRef {
	if t == nil {
		return nil
	}
	cp := *t
	if t.Map != nil {
		mp := *t.Map
		mp.Key = m.typeRef(home, t.Map.Key, params)
		mp.Value = m.typeRef(home, t.Map.Value, params)
		cp.Map = &mp
	}
	cp.Named = m.ref(home, t.Named, params, semantic.TypeRefDecls)
	return &cp
}

// members returns copies of body's fields and mixins with their refs renamed,
// leaving the analysed declarations untouched.
func (m *merger) members(home string, body []ast.TypeMember, params map[string]string) []ast.TypeMember {
	out := make([]ast.TypeMember, 0, len(body))
	for _, member := range body {
		switch v := member.(type) {
		case *ast.Field:
			cp := *v
			cp.Type = m.typeRef(home, v.Type, params)
			out = append(out, &cp)
		case *ast.Mixin:
			cp := *v
			cp.Ref = m.ref(home, v.Ref, params, semantic.TypeRefDecls)
			out = append(out, &cp)
		default:
			out = append(out, member)
		}
	}
	return out
}

// service copies si with each method's request, response and `@errors`
// names renamed.
func (m *merger) service(home string, si *semantic.ServiceInfo) *semantic.ServiceInfo {
	out := *si
	out.Extends = make([]*ast.ServiceDecl, len(si.Extends))
	for i, e := range si.Extends {
		ec := *e
		ec.Decorators = m.errorsDecorators(home, e.Decorators)
		out.Extends[i] = &ec
	}
	out.Methods = make([]*ast.Method, len(si.Methods))
	for i, method := range si.Methods {
		cp := *method
		cp.Request = m.ref(home, method.Request, nil, semantic.TypeRefDecls)
		if method.Response != nil && method.Response.Type != nil {
			resp := *method.Response
			resp.Type = m.ref(home, method.Response.Type, nil, semantic.TypeRefDecls)
			cp.Response = &resp
		}
		cp.Decorators = m.errorsDecorators(home, method.Decorators)
		out.Methods[i] = &cp
	}
	return &out
}

// errorsDecorators returns ds with every error each `@errors` names renamed,
// the elements of `@errors([A, B])` included.
func (m *merger) errorsDecorators(home string, ds []*ast.Decorator) []*ast.Decorator {
	out := slices.Clone(ds)
	for i, d := range ds {
		if d == nil || d.Name != "errors" {
			continue
		}
		dc := *d
		dc.Args = make([]*ast.DecoratorArg, len(d.Args))
		for j, a := range d.Args {
			ac := *a
			ac.Value = m.errorName(home, a.Value)
			dc.Args[j] = &ac
		}
		out[i] = &dc
	}
	return out
}

// errorName returns e, an `@errors` argument written in package home, with
// the error it names renamed, each element of an array.
func (m *merger) errorName(home string, e ast.Expr) ast.Expr {
	switch v := e.(type) {
	case *ast.ArrayLit:
		cp := *v
		cp.Elements = make([]ast.Expr, len(v.Elements))
		for i, el := range v.Elements {
			cp.Elements[i] = m.errorName(home, el)
		}
		return &cp
	case *ast.IdentExpr:
		n := m.ref(home, &ast.NamedTypeRef{Pos: v.Pos, Name: v.Name}, nil, semantic.ErrorDecls)
		cp := *v
		cp.Name = n.Name
		return &cp
	}
	return e
}
