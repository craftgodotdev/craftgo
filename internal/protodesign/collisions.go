package protodesign

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// checkCollisions rejects two design files in one directory with different
// proto packages, two services with one output directory, and two RPCs of a
// service with one file name.
func (s *Set) checkCollisions() error {
	var errs []string
	pkgByDir := map[string]string{}
	fileByDir := map[string]string{}
	for _, name := range s.names {
		f := s.plugin.FilesByPath[name]
		if f == nil {
			continue
		}
		dir, pkg := path.Dir(name), string(f.Desc.Package())
		if prev, ok := pkgByDir[dir]; ok && prev != pkg {
			errs = append(errs, fmt.Sprintf("%s declares package %s but %s in the same directory declares %s - one directory, one package", name, pkg, fileByDir[dir], prev))
			continue
		}
		pkgByDir[dir], fileByDir[dir] = pkg, name
	}
	svcByDir := map[string]*Service{}
	for _, svc := range s.Services {
		if prev, ok := svcByDir[svc.Dir]; ok {
			errs = append(errs, fmt.Sprintf("services %s and %s both generate into directory %q - rename one", prev.FullName, svc.FullName, svc.Dir))
			continue
		}
		svcByDir[svc.Dir] = svc
		byFile := map[string]*Method{}
		for _, m := range svc.Methods {
			if prev, ok := byFile[m.File]; ok {
				errs = append(errs, fmt.Sprintf("%s: rpcs %s and %s both generate file %q - rename one", svc.FullName, prev.Name, m.Name, m.File+".go"))
				continue
			}
			byFile[m.File] = m
		}
	}
	if len(errs) == 0 {
		return nil
	}
	sort.Strings(errs)
	return fmt.Errorf("proto errors:\n  %s", strings.Join(errs, "\n  "))
}
