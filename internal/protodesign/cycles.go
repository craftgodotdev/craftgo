package protodesign

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// importStep is one proto import that makes one Go package import another.
type importStep struct{ from, to string }

// importCycles describes each ring of Go packages that the design protos import one
// another in, with the proto imports that close it: one Go package per directory turns
// protos that import one way, such as a/a2 → b/b1 → a/a1, into packages a and b
// importing each other, which Go refuses.
func (s *Set) importCycles() []string {
	pkgOf := map[string]string{}
	var design []string
	for _, f := range s.plugin.Files {
		pkgOf[f.Desc.Path()] = string(f.GoImportPath)
		if f.Generate {
			design = append(design, string(f.GoImportPath))
		}
	}
	edges := map[string]map[string]importStep{}
	for _, f := range s.plugin.Files {
		from := string(f.GoImportPath)
		imports := f.Desc.Imports()
		for i := range imports.Len() {
			imp := imports.Get(i)
			to, ok := pkgOf[imp.Path()]
			if !ok || to == from {
				continue
			}
			if edges[from] == nil {
				edges[from] = map[string]importStep{}
			}
			if _, seen := edges[from][to]; !seen {
				edges[from][to] = importStep{from: f.Desc.Path(), to: imp.Path()}
			}
		}
	}
	slices.Sort(design)
	reported := map[string]bool{}
	var out []string
	for _, start := range slices.Compact(design) {
		if reported[start] {
			continue
		}
		pkgs := ringFrom(start, edges)
		if pkgs == nil {
			continue
		}
		var steps []string
		for i, p := range pkgs {
			reported[p] = true
			step := edges[p][pkgs[(i+1)%len(pkgs)]]
			steps = append(steps, step.from+" imports "+step.to)
		}
		why := "the protos that share a go_package are one Go package, so regroup them"
		if s.opts.pbEnabled() {
			why = "each proto directory is one Go package, so move protos between directories"
		}
		out = append(out, fmt.Sprintf("Go packages %s import one another, which Go refuses: %s - %s until the packages import one way",
			joinAnd(pkgs), joinAnd(steps), why))
	}
	return out
}

// ringFrom returns the shortest ring of packages that leads from start back to it, start first,
// or nil when start is on none.
func ringFrom(start string, edges map[string]map[string]importStep) []string {
	parent := map[string]string{}
	queue := []string{start}
	for len(queue) > 0 {
		at := queue[0]
		queue = queue[1:]
		for _, next := range slices.Sorted(maps.Keys(edges[at])) {
			if next == start {
				ring := []string{at}
				for p := at; p != start; {
					p = parent[p]
					ring = append(ring, p)
				}
				slices.Reverse(ring)
				return ring
			}
			if _, seen := parent[next]; !seen {
				parent[next] = at
				queue = append(queue, next)
			}
		}
	}
	return nil
}

// joinAnd joins items as prose: `a`, `a and b`, `a, b and c`.
func joinAnd(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}
