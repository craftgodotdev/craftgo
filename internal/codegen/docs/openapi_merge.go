package docs

import (
	"maps"
	"slices"
	"sort"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

type symbolKey struct{ pkg, name string }

// projectResolveTable maps each package's declaration names to merged names,
// `<PascalPkg><Name>` when two packages declare one, and lists the packages.
func projectResolveTable(proj *semantic.Project) (map[symbolKey]string, []string) {
	pkgNames := proj.PackageNames()
	collide := func(name string) bool {
		count := 0
		for _, pn := range pkgNames {
			p := proj.Packages[pn]
			if p == nil {
				continue
			}
			if hasAnyDecl(p, name) {
				count++
				if count >= 2 {
					return true
				}
			}
		}
		return false
	}
	resolve := map[symbolKey]string{}
	for _, pkgName := range pkgNames {
		p := proj.Packages[pkgName]
		if p == nil {
			continue
		}
		for _, name := range allDeclNames(p) {
			final := name
			if collide(name) {
				final = idents.PascalCase(pkgName) + name
			}
			resolve[symbolKey{pkg: pkgName, name: name}] = final
		}
	}
	return resolve, pkgNames
}

// projectMergeCollisions returns the merged names two declarations share,
// such as `shared.User` renamed to the `SharedUser` that package api declares.
func projectMergeCollisions(proj *semantic.Project) []string {
	resolve, _ := projectResolveTable(proj)
	owners := map[string]map[string]bool{}
	for k, final := range resolve {
		if owners[final] == nil {
			owners[final] = map[string]bool{}
		}
		owners[final][k.pkg+"."+k.name] = true
	}
	var dups []string
	for final, set := range owners {
		if len(set) >= 2 {
			dups = append(dups, final)
		}
	}
	sort.Strings(dups)
	return dups
}

func mergeProjectForOpenAPI(proj *semantic.Project) *semantic.Package {
	out := &semantic.Package{
		Types:       map[string]*ast.TypeDecl{},
		Enums:       map[string]*ast.EnumDecl{},
		Errors:      map[string]*ast.ErrorDecl{},
		Scalars:     map[string]*ast.ScalarDecl{},
		Middlewares: map[string]*ast.MiddlewareDecl{},
		Services:    map[string]*semantic.ServiceInfo{},
	}
	resolve, pkgNames := projectResolveTable(proj)
	if len(pkgNames) > 0 {
		out.Name = pkgNames[0]
	}

	// rewriteRef returns a copy of n, a ref written in srcPkg, carrying its
	// merged name, or n itself when the name does not change.
	rewriteRef := func(srcPkg string, n *ast.NamedTypeRef) *ast.NamedTypeRef {
		if n == nil || n.Name == nil {
			return n
		}
		switch len(n.Name.Parts) {
		case 1:
			final, ok := resolve[symbolKey{pkg: srcPkg, name: n.Name.Parts[0]}]
			if !ok || final == n.Name.Parts[0] {
				return n
			}
			cp := *n
			cp.Name = &ast.QualifiedIdent{Pos: n.Name.Pos, Parts: []string{final}}
			return &cp
		case 2:
			final, ok := resolve[symbolKey{pkg: n.Name.Parts[0], name: n.Name.Parts[1]}]
			if !ok {
				return n
			}
			cp := *n
			cp.Name = &ast.QualifiedIdent{Pos: n.Name.Pos, Parts: []string{final}}
			return &cp
		}
		return n
	}

	for _, pkgName := range pkgNames {
		p := proj.Packages[pkgName]
		if p == nil {
			continue
		}
		for _, k := range slices.Sorted(maps.Keys(p.Types)) {
			td := cloneTypeDecl(p.Types[k], resolve[symbolKey{pkg: pkgName, name: k}], pkgName, rewriteRef)
			out.Types[td.Name] = td
		}
		for _, k := range slices.Sorted(maps.Keys(p.Enums)) {
			ed := *p.Enums[k]
			ed.Name = resolve[symbolKey{pkg: pkgName, name: k}]
			out.Enums[ed.Name] = &ed
		}
		for _, k := range slices.Sorted(maps.Keys(p.Errors)) {
			ed := cloneErrorDecl(p.Errors[k], resolve[symbolKey{pkg: pkgName, name: k}], pkgName, rewriteRef)
			out.Errors[ed.Name] = ed
		}
		for _, k := range slices.Sorted(maps.Keys(p.Scalars)) {
			sd := *p.Scalars[k]
			sd.Name = resolve[symbolKey{pkg: pkgName, name: k}]
			out.Scalars[sd.Name] = &sd
		}
		// Services merge by name; one without methods is left out.
		for name, si := range p.Services {
			if len(si.Methods) == 0 {
				continue
			}
			out.Services[name] = cloneServiceInfo(si, pkgName, rewriteRef)
		}
		maps.Copy(out.Middlewares, p.Middlewares)
	}
	return out
}

// hasAnyDecl reports whether p declares a type, enum, error or scalar
// called name.
func hasAnyDecl(p *semantic.Package, name string) bool {
	if _, ok := p.Types[name]; ok {
		return true
	}
	if _, ok := p.Enums[name]; ok {
		return true
	}
	if _, ok := p.Errors[name]; ok {
		return true
	}
	if _, ok := p.Scalars[name]; ok {
		return true
	}
	return false
}

// allDeclNames returns the type, enum, error and scalar names of p, sorted.
func allDeclNames(p *semantic.Package) []string {
	seen := map[string]bool{}
	add := func(names ...string) {
		for _, n := range names {
			seen[n] = true
		}
	}
	for n := range p.Types {
		add(n)
	}
	for n := range p.Enums {
		add(n)
	}
	for n := range p.Errors {
		add(n)
	}
	for n := range p.Scalars {
		add(n)
	}
	return slices.Sorted(maps.Keys(seen))
}

// cloneTypeDecl copies td as newName, its body refs renamed by rewrite.
func cloneTypeDecl(td *ast.TypeDecl, newName, srcPkg string, rewrite func(string, *ast.NamedTypeRef) *ast.NamedTypeRef) *ast.TypeDecl {
	cp := *td
	cp.Name = newName
	cp.Body = rewriteMembers(td.Body, srcPkg, rewrite)
	return &cp
}

// cloneErrorDecl copies ed as newName, its body refs renamed by rewrite.
func cloneErrorDecl(ed *ast.ErrorDecl, newName, srcPkg string, rewrite func(string, *ast.NamedTypeRef) *ast.NamedTypeRef) *ast.ErrorDecl {
	cp := *ed
	cp.Name = newName
	cp.Body = rewriteMembers(ed.Body, srcPkg, rewrite)
	return &cp
}

// rewriteMembers returns copies of members with their refs renamed by
// rewrite, leaving the analysed declarations untouched.
func rewriteMembers(members []ast.TypeMember, srcPkg string, rewrite func(string, *ast.NamedTypeRef) *ast.NamedTypeRef) []ast.TypeMember {
	out := make([]ast.TypeMember, 0, len(members))
	for _, m := range members {
		switch v := m.(type) {
		case *ast.Field:
			cp := *v
			cp.Type = rewriteTypeRef(v.Type, srcPkg, rewrite)
			out = append(out, &cp)
		case *ast.Mixin:
			cp := *v
			nr := rewrite(srcPkg, v.Ref)
			// rewrite renames only the ref itself, not its type arguments.
			nr = rewriteNamedArgs(nr, srcPkg, rewrite)
			cp.Ref = nr
			out = append(out, &cp)
		default:
			out = append(out, m)
		}
	}
	return out
}

// rewriteTypeRef returns a copy of t with every named ref in it renamed by
// rewrite, map entries and type arguments included.
func rewriteTypeRef(t *ast.TypeRef, srcPkg string, rewrite func(string, *ast.NamedTypeRef) *ast.NamedTypeRef) *ast.TypeRef {
	if t == nil {
		return nil
	}
	cp := *t
	if t.Map != nil {
		mp := *t.Map
		mp.Key = rewriteTypeRef(t.Map.Key, srcPkg, rewrite)
		mp.Value = rewriteTypeRef(t.Map.Value, srcPkg, rewrite)
		cp.Map = &mp
	}
	if t.Named != nil {
		named := rewrite(srcPkg, t.Named)
		named = rewriteNamedArgs(named, srcPkg, rewrite)
		cp.Named = named
	}
	return &cp
}

// cloneServiceInfo copies si with each method's request, response and
// `@errors` refs renamed by rewrite, type arguments included.
func cloneServiceInfo(si *semantic.ServiceInfo, srcPkg string, rewrite func(string, *ast.NamedTypeRef) *ast.NamedTypeRef) *semantic.ServiceInfo {
	out := *si
	out.Methods = make([]*ast.Method, len(si.Methods))
	for i, m := range si.Methods {
		cp := *m
		if m.Request != nil {
			cp.Request = rewriteNamedTypeRef(m.Request, srcPkg, rewrite)
		}
		if m.Response != nil && m.Response.Type != nil {
			respCopy := *m.Response
			respCopy.Type = rewriteNamedTypeRef(m.Response.Type, srcPkg, rewrite)
			cp.Response = &respCopy
		}
		cp.Decorators = rewriteErrorDecorators(m.Decorators, srcPkg, rewrite)
		out.Methods[i] = &cp
	}
	return &out
}

// rewriteErrorDecorators renames, through rewrite, each identifier argument
// of every `@errors` in ds (`Dup` → `ADup`).
func rewriteErrorDecorators(ds []*ast.Decorator, srcPkg string, rewrite func(string, *ast.NamedTypeRef) *ast.NamedTypeRef) []*ast.Decorator {
	if len(ds) == 0 {
		return ds
	}
	out := make([]*ast.Decorator, len(ds))
	for i, d := range ds {
		if d == nil || d.Name != "errors" {
			out[i] = d
			continue
		}
		dc := *d
		dc.Args = make([]*ast.DecoratorArg, len(d.Args))
		for j, a := range d.Args {
			id, ok := a.Value.(*ast.IdentExpr)
			if !ok || id.Name == nil {
				dc.Args[j] = a
				continue
			}
			named := rewrite(srcPkg, &ast.NamedTypeRef{Pos: id.Pos, Name: id.Name})
			ac := *a
			idc := *id
			idc.Name = named.Name
			ac.Value = &idc
			dc.Args[j] = &ac
		}
		out[i] = &dc
	}
	return out
}

// rewriteNamedTypeRef is [rewriteTypeRef] for a bare named ref.
func rewriteNamedTypeRef(n *ast.NamedTypeRef, srcPkg string, rewrite func(string, *ast.NamedTypeRef) *ast.NamedTypeRef) *ast.NamedTypeRef {
	if n == nil {
		return nil
	}
	return rewriteNamedArgs(rewrite(srcPkg, n), srcPkg, rewrite)
}

// rewriteNamedArgs returns a copy of n with its type arguments renamed by
// rewrite, or n when it has none.
func rewriteNamedArgs(n *ast.NamedTypeRef, srcPkg string, rewrite func(string, *ast.NamedTypeRef) *ast.NamedTypeRef) *ast.NamedTypeRef {
	if n == nil || len(n.Args) == 0 {
		return n
	}
	args := make([]*ast.TypeRef, len(n.Args))
	for i, a := range n.Args {
		args[i] = rewriteTypeRef(a, srcPkg, rewrite)
	}
	cp := *n
	cp.Args = args
	return &cp
}
