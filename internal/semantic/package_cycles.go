package semantic

import (
	"maps"
	"slices"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// checkPackageCycles rejects packages whose type and error declarations
// reference each other in a cycle: each package's types are one Go package,
// and Go rejects an import cycle. In name order, each package on no reported
// cycle reports the shortest cycle through it, at the cycle's first reference.
func (c *projectChecks) checkPackageCycles() {
	refs := c.packageRefs()
	reported := map[string]bool{}
	for _, start := range c.proj.PackageNames() {
		if reported[start] {
			continue
		}
		path := shortestCycle(refs, start)
		if path == nil {
			continue
		}
		for _, name := range path {
			reported[name] = true
		}
		d := c.diag(refs[path[0]][path[1]], lexer.SeverityError, CodeRefPackageCycle,
			"packages reference each other's types in a cycle (%s) - each package's types are one Go package and Go rejects an import cycle; move the types they share into one package",
			strings.Join(path, " -> "))
		for i := 1; i+1 < len(path); i++ {
			d.Related = append(d.Related, related(refs[path[i]][path[i+1]], path[i]+" references "+path[i+1]+" here")...)
		}
	}
}

// packageRefs returns, for each package, the other packages its type and
// error declarations name, with the first position naming each.
func (c *projectChecks) packageRefs() map[string]map[string]lexer.Position {
	out := map[string]map[string]lexer.Position{}
	for _, from := range c.proj.PackageNames() {
		for _, d := range c.proj.Packages[from].Decls(TypeDecls | ErrorDecls) {
			walkTypeRefs(d, func(n *ast.NamedTypeRef, _ []string, _ bool) {
				if n.Name == nil || len(n.Name.Parts) != 2 {
					return
				}
				to := n.Name.Parts[0]
				if to == from || c.proj.Packages[to] == nil {
					return
				}
				if out[from] == nil {
					out[from] = map[string]lexer.Position{}
				}
				if prev, seen := out[from][to]; !seen || comparePos(n.Pos, prev) < 0 {
					out[from][to] = n.Pos
				}
			})
		}
	}
	return out
}

// shortestCycle returns the shortest path of refs from start back to start,
// both ends included, or nil when start is on no cycle.
func shortestCycle(refs map[string]map[string]lexer.Position, start string) []string {
	parent := map[string]string{}
	queue := []string{start}
	for len(queue) > 0 {
		from := queue[0]
		queue = queue[1:]
		for _, to := range slices.Sorted(maps.Keys(refs[from])) {
			if to == start {
				path := []string{start}
				for at := from; at != start; at = parent[at] {
					path = append(path, at)
				}
				slices.Reverse(path[1:])
				return append(path, start)
			}
			if _, seen := parent[to]; seen {
				continue
			}
			parent[to] = from
			queue = append(queue, to)
		}
	}
	return nil
}
