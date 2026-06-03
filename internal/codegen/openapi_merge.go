// Multi-package merge + clone/rewrite helpers for the OpenAPI document builder.
package codegen

import (
	"sort"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

func mergeProjectForOpenAPI(proj *semantic.Project) *semantic.Package {
	out := &semantic.Package{
		Types:       map[string]*ast.TypeDecl{},
		Enums:       map[string]*ast.EnumDecl{},
		Errors:      map[string]*ast.ErrorDecl{},
		Scalars:     map[string]*ast.ScalarDecl{},
		Middlewares: map[string]*ast.MiddlewareDecl{},
		Services:    map[string]*semantic.ServiceInfo{},
	}
	pkgNames := make([]string, 0, len(proj.Packages))
	for n := range proj.Packages {
		if n != "" {
			pkgNames = append(pkgNames, n)
		}
	}
	sort.Strings(pkgNames)
	if len(pkgNames) > 0 {
		out.Name = pkgNames[0]
	}

	// Build the name-resolution table: for each (pkgName, origName)
	// tuple, the merged identifier the schema lives under. Conflicts
	// are detected by membership-counting across all packages: a
	// name is "shared" when it appears in 2+ packages.
	type symbolKey struct{ pkg, name string }
	resolve := map[symbolKey]string{}
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
	for _, pkgName := range pkgNames {
		p := proj.Packages[pkgName]
		if p == nil {
			continue
		}
		for _, name := range allDeclNames(p) {
			final := name
			if collide(name) {
				final = pascalCase(pkgName) + name
			}
			resolve[symbolKey{pkg: pkgName, name: name}] = final
		}
	}

	// Clone every decl into the merged package, rewriting the decl's
	// body type refs so `$ref` resolution still lines up. rewriteRef
	// takes the SOURCE package + a NamedTypeRef and returns either
	// the original ref (no rewrite needed) or a new ref with the
	// resolved single-part name.
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
		for _, k := range sortedKeys(p.Types) {
			td := cloneTypeDecl(p.Types[k], resolve[symbolKey{pkg: pkgName, name: k}], pkgName, rewriteRef)
			out.Types[td.Name] = td
		}
		for _, k := range sortedKeys(p.Enums) {
			ed := *p.Enums[k]
			ed.Name = resolve[symbolKey{pkg: pkgName, name: k}]
			out.Enums[ed.Name] = &ed
		}
		for _, k := range sortedKeys(p.Errors) {
			ed := cloneErrorDecl(p.Errors[k], resolve[symbolKey{pkg: pkgName, name: k}], pkgName, rewriteRef)
			out.Errors[ed.Name] = ed
		}
		for _, k := range sortedKeys(p.Scalars) {
			sd := *p.Scalars[k]
			sd.Name = resolve[symbolKey{pkg: pkgName, name: k}]
			out.Scalars[sd.Name] = &sd
		}
		for name, si := range p.Services {
			final := name
			if _, dup := out.Services[final]; dup {
				final = pascalCase(pkgName) + name
			}
			out.Services[final] = cloneServiceInfo(si, pkgName, rewriteRef)
		}
		for name, md := range p.Middlewares {
			final := name
			if _, dup := out.Middlewares[final]; dup {
				final = pascalCase(pkgName) + name
			}
			out.Middlewares[final] = md
		}
	}
	return out
}

// hasAnyDecl reports whether p has a type/enum/error/scalar named
// `name`. Used by the collision detector during the OpenAPI merge.
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

// allDeclNames returns every type/enum/error/scalar name in p, in
// stable alphabetical order. Used to seed the resolve table during
// the OpenAPI merge.
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
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// sortedKeys returns the keys of m in alphabetical order. Used by
// the merge to produce deterministic schema ordering.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// cloneTypeDecl deep-copies td and rewrites every named type ref in
// its body using rewrite (which carries srcPkg context). The
// returned decl is safe to mutate further.
func cloneTypeDecl(td *ast.TypeDecl, newName, srcPkg string, rewrite func(string, *ast.NamedTypeRef) *ast.NamedTypeRef) *ast.TypeDecl {
	cp := *td
	cp.Name = newName
	cp.Body = rewriteMembers(td.Body, srcPkg, rewrite)
	return &cp
}

// cloneErrorDecl mirrors [cloneTypeDecl] for errors.
func cloneErrorDecl(ed *ast.ErrorDecl, newName, srcPkg string, rewrite func(string, *ast.NamedTypeRef) *ast.NamedTypeRef) *ast.ErrorDecl {
	cp := *ed
	cp.Name = newName
	cp.Body = rewriteMembers(ed.Body, srcPkg, rewrite)
	return &cp
}

// rewriteMembers walks members, deep-copying every Field and Mixin
// with rewritten type refs. The original list is left untouched so
// upstream semantic results stay valid.
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
			// rewrite touches only the ref's NAME; its generic args (a
			// cross-pkg arg like `lib.Owner` in `lib.Page<lib.Owner>`)
			// must be rewritten too, or the merged generic instance's
			// element `$ref` dangles at `lib.Owner`.
			if nr != nil && len(nr.Args) > 0 {
				nrc := *nr
				nrc.Args = make([]*ast.TypeRef, len(nr.Args))
				for i, a := range nr.Args {
					nrc.Args[i] = rewriteTypeRef(a, srcPkg, rewrite)
				}
				nr = &nrc
			}
			cp.Ref = nr
			out = append(out, &cp)
		default:
			out = append(out, m)
		}
	}
	return out
}

// rewriteTypeRef descends into a TypeRef and applies rewrite to every
// embedded NamedTypeRef, including map keys/values and generic args.
// Returns a fresh tree so the caller can safely mutate further.
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
		// Recurse into generic args so a nested cross-pkg ref is
		// also rewritten.
		if named != nil && len(named.Args) > 0 {
			args := make([]*ast.TypeRef, len(named.Args))
			for i, a := range named.Args {
				args[i] = rewriteTypeRef(a, srcPkg, rewrite)
			}
			ncp := *named
			ncp.Args = args
			named = &ncp
		}
		cp.Named = named
	}
	return &cp
}

// cloneServiceInfo shallow-clones a ServiceInfo and rewrites every
// method's request/response type so $ref resolution lines up after
// the merge's rename pass. Both the top-level NamedTypeRef name AND
// any generic args are rewritten - without arg rewriting, a method
// response like `Envelope<Order>` keeps a stale `Order` arg even when
// the merge renamed it to `ScalarsOrder`, producing two divergent
// generic instantiation components.
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

// rewriteErrorDecorators rewrites the error refs inside every
// `@errors(...)` decorator so they track the merge's rename pass. When
// two packages declare an error of the same name, the merge renames both
// (e.g. `Dup` → `ADup` / `BDup`) and stores each under its renamed key in
// the merged Errors map. The per-operation response builder looks the
// error up by the decorator's trailing segment, so without rewriting the
// decorator that lookup misses the renamed schema and the error silently
// drops from the OpenAPI responses. Decorators other than `@errors` pass
// through unchanged, and a ref the rename table leaves alone returns its
// original node.
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

// rewriteNamedTypeRef rewrites a NamedTypeRef itself plus the args
// recursively. Used by the service-clone path where the request and
// response types are stored as *NamedTypeRef (not wrapped in TypeRef),
// so [rewriteTypeRef] cannot be reused directly.
func rewriteNamedTypeRef(n *ast.NamedTypeRef, srcPkg string, rewrite func(string, *ast.NamedTypeRef) *ast.NamedTypeRef) *ast.NamedTypeRef {
	if n == nil {
		return nil
	}
	named := rewrite(srcPkg, n)
	if named != nil && len(named.Args) > 0 {
		args := make([]*ast.TypeRef, len(named.Args))
		for i, a := range named.Args {
			args[i] = rewriteTypeRef(a, srcPkg, rewrite)
		}
		ncp := *named
		ncp.Args = args
		named = &ncp
	}
	return named
}

// pascalCase converts a DSL package name (commonly lowercase or
// kebab-cased) into its PascalCase form for OpenAPI schema prefixing.
// Empty / single-character inputs are passed through with the first
// rune uppercased.
func pascalCase(s string) string {
	if s == "" {
		return ""
	}
	var b []byte
	upNext := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '-' || c == '_' || c == '/' {
			upNext = true
			continue
		}
		if upNext {
			if c >= 'a' && c <= 'z' {
				c -= 'a' - 'A'
			}
			upNext = false
		}
		b = append(b, c)
	}
	return string(b)
}
