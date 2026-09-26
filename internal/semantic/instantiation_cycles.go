package semantic

import (
	"maps"
	"slices"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// typeParamSlot is the index-th type parameter of the generic type typ
// declared in package pkg.
type typeParamSlot struct {
	pkg, typ string
	index    int
}

// paramFlow is an instantiation site in the body of the generic type host
// whose argument for the type parameter to holds host's parameter param
// (from): the parameter itself, or, when grows, a type built from it.
type paramFlow struct {
	host     *ast.TypeDecl
	site     *ast.NamedTypeRef
	param    string
	from, to typeParamSlot
	grows    bool
}

// checkInstantiationCycles reports each site of [Project.endlessFlows]: an
// instantiation in a generic type's body that passes one of the type's
// parameters, inside a larger type, to a parameter whose flows lead back to
// it. A parameter passed on unchanged (`kids Tree<T>[]`) closes the recursion.
func (c *projectChecks) checkInstantiationCycles() {
	reported := map[*ast.NamedTypeRef]bool{}
	for _, f := range c.proj.endlessFlows {
		if reported[f.site] {
			continue
		}
		reported[f.site] = true
		c.diag(f.site.Pos, lexer.SeverityError, CodeGenericInstantiationCycle,
			"type %s instantiates %s, which feeds its type parameter %s back into %s inside a larger type - every instance needs a larger one, so Go rejects the instantiation cycle and the OpenAPI components would never end. Pass the type parameter unchanged or a concrete type.",
			f.host.Name, f.site, f.param, f.host.Name)
	}
}

// endlessParamFlows returns the flows of p that grow on a cycle: every
// instance of their host needs a larger one, so its instantiations never end.
func (p *Project) endlessParamFlows() []paramFlow {
	flows := p.paramFlows()
	next := map[typeParamSlot][]typeParamSlot{}
	for _, f := range flows {
		next[f.from] = append(next[f.from], f.to)
	}
	var endless []paramFlow
	for _, f := range flows {
		if f.grows && slotReaches(next, f.to, f.from) {
			endless = append(endless, f)
		}
	}
	return endless
}

// instantiatesEndlessly reports whether td hosts one of [Project.endlessFlows].
func (p *Project) instantiatesEndlessly(td *ast.TypeDecl) bool {
	return slices.ContainsFunc(p.endlessFlows, func(f paramFlow) bool { return f.host == td })
}

// walkedInstance returns the arguments a walk through the structs a type
// reaches expands td, which n names in package pkg, with, and the key it
// marks the instance seen under: n's own arguments, or none under td's name
// when td instantiates itself endlessly, so that the walk ends.
func (p *Project) walkedInstance(pkg *Package, td *ast.TypeDecl, n *ast.NamedTypeRef) (args []*ast.TypeRef, key string) {
	if p.instantiatesEndlessly(td) {
		return nil, pkg.Name + "." + td.Name
	}
	return n.Args, pkg.Name + "." + n.String()
}

// paramFlows returns the flow of each type parameter of each generic type
// into the instantiations its body holds: field types, mixins, map keys and
// values and nested generic arguments.
func (p *Project) paramFlows() []paramFlow {
	var flows []paramFlow
	for _, pkgName := range p.PackageNames() {
		pkg := p.Packages[pkgName]
		for _, name := range slices.Sorted(maps.Keys(pkg.Types)) {
			host := pkg.Types[name]
			if len(host.TypeParams) == 0 {
				continue
			}
			walkTypeRefs(host, func(n *ast.NamedTypeRef, _ []string, _ bool) {
				targetPkg, sym := p.resolve(pkgName, n.Name)
				if targetPkg == nil || targetPkg.Types[sym] == nil {
					return
				}
				target := targetPkg.Types[sym]
				for i, arg := range n.Args[:min(len(n.Args), len(target.TypeParams))] {
					for j, param := range host.TypeParams {
						if !mentionsTypeParam(arg, param) {
							continue
						}
						flows = append(flows, paramFlow{
							host:  host,
							site:  n,
							param: param,
							from:  typeParamSlot{pkg: pkgName, typ: name, index: j},
							to:    typeParamSlot{pkg: targetPkg.Name, typ: sym, index: i},
							grows: arg.Array || arg.Optional || !typeParamNamed(arg, []string{param}),
						})
					}
				}
			})
		}
	}
	return flows
}

// mentionsTypeParam reports whether t names the type parameter param, itself
// or inside a map, an array or a generic argument.
func mentionsTypeParam(t *ast.TypeRef, param string) bool {
	found := false
	t.WalkNamedRefs(func(n *ast.NamedTypeRef) {
		found = found || (n.Name != nil && len(n.Name.Parts) == 1 && n.Name.Parts[0] == param)
	})
	return found
}

// slotReaches reports whether the flows in next lead from slot from to slot
// to, from itself included.
func slotReaches(next map[typeParamSlot][]typeParamSlot, from, to typeParamSlot) bool {
	seen := map[typeParamSlot]bool{from: true}
	queue := []typeParamSlot{from}
	for len(queue) > 0 {
		at := queue[0]
		queue = queue[1:]
		if at == to {
			return true
		}
		for _, n := range next[at] {
			if !seen[n] {
				seen[n] = true
				queue = append(queue, n)
			}
		}
	}
	return false
}
