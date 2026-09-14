// A projection of a design: the part of it one deployable runs.
package semantic

import (
	"fmt"
	"sort"
	"strings"
)

// Projection returns the part of the design a deployable generates its
// application half from - its handlers, logic stubs, routes, wiring and
// container members. Each entry in `services` names one service as
// `<package>.<Service>`; an empty selection projects the whole design,
// which is what a project generating everything asks for.
//
// Only the services and their consumers are narrowed. Every type, enum,
// error, scalar and event contract stays, because the contract half is
// generated from [Project.Design] whatever the selection: it is the
// design's shared vocabulary, two deployables of one design must emit it
// identically, and a service this deployable runs may well consume a
// contract another one publishes.
func (p *Project) Projection(services []string) (*Project, error) {
	if p == nil || len(services) == 0 {
		return p, nil
	}
	selected := map[string]map[string]bool{}
	for _, entry := range services {
		pkgName, svcName, _ := strings.Cut(entry, ".")
		pkg := p.Packages[pkgName]
		if pkg == nil || pkg.Services[svcName] == nil {
			return nil, fmt.Errorf("output.services names %s, which the design does not declare - %s", entry, p.nearMisses(pkgName, svcName))
		}
		if selected[pkgName] == nil {
			selected[pkgName] = map[string]bool{}
		}
		selected[pkgName][svcName] = true
	}
	out := &Project{Root: p.Root, Packages: make(map[string]*Package, len(p.Packages)), design: p.Design()}
	for name, pkg := range p.Packages {
		if pkg == nil {
			continue
		}
		projected := *pkg
		projected.Services = map[string]*ServiceInfo{}
		projected.Consumers = map[string]*ConsumerInfo{}
		for svcName, svc := range pkg.Services {
			if selected[name][svcName] {
				projected.Services[svcName] = svc
			}
		}
		for cName, ci := range pkg.Consumers {
			if ci != nil && selected[name][ci.Service] {
				projected.Consumers[cName] = ci
			}
		}
		out.Packages[name] = &projected
	}
	return out, nil
}

// Design returns the whole design a projection was taken from, and the
// project itself when it projects everything. The contract half is
// generated from it, so a deployable running two of a design's services
// still emits the same library as one running all of them.
func (p *Project) Design() *Project {
	if p == nil || p.design == nil {
		return p
	}
	return p.design
}

// nearMisses phrases what a selection entry might have meant: the same
// service name in another package, or another service in the one it
// names. Falls back to every service the design declares - a design is
// written by hand, so the whole list is readable.
func (p *Project) nearMisses(pkgName, svcName string) string {
	var near []string
	for name, pkg := range p.Packages {
		if pkg == nil {
			continue
		}
		for declared := range pkg.Services {
			if name == pkgName || strings.EqualFold(declared, svcName) {
				near = append(near, qualifiedService(name, declared))
			}
		}
	}
	if len(near) == 0 {
		for name, pkg := range p.Packages {
			if pkg == nil {
				continue
			}
			for declared := range pkg.Services {
				near = append(near, qualifiedService(name, declared))
			}
		}
		sort.Strings(near)
		if len(near) == 0 {
			return "the design declares no service"
		}
		return "the design declares " + strings.Join(near, ", ")
	}
	sort.Strings(near)
	return "did you mean " + strings.Join(near, ", ") + "?"
}

// qualifiedService is a service as `output.services` names it. A design
// whose files declare no package has nothing to qualify with, so the
// bare name is shown and the entry can never match - the message is the
// place that says so.
func qualifiedService(pkgName, svcName string) string {
	if pkgName == "" {
		return svcName
	}
	return pkgName + "." + svcName
}
