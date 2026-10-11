package protodesign

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// checkCollisions rejects two design files in one directory with different
// proto packages, two services with one output directory, two RPCs of a
// service with one file name, and Go packages the design protos import in a
// ring.
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
	// Names are compared case-folded: a case-insensitive file system, macOS's and Windows'
	// default, holds two names that differ only in case as one.
	svcByDir := map[string]*Service{}
	for _, svc := range s.Services {
		if prev, ok := svcByDir[strings.ToLower(svc.Dir)]; ok {
			if prev.Dir == svc.Dir {
				errs = append(errs, fmt.Sprintf("services %s and %s both generate into directory %q - rename one", prev.FullName, svc.FullName, svc.Dir))
			} else {
				errs = append(errs, fmt.Sprintf("services %s and %s generate into directories %q and %q, one directory on a case-insensitive file system - rename one", prev.FullName, svc.FullName, prev.Dir, svc.Dir))
			}
			continue
		}
		svcByDir[strings.ToLower(svc.Dir)] = svc
		byFile := map[string]*Method{}
		for _, m := range svc.Methods {
			if prev, ok := byFile[strings.ToLower(m.File)]; ok {
				if prev.File == m.File {
					errs = append(errs, fmt.Sprintf("%s: rpcs %s and %s both generate file %q - rename one", svc.FullName, prev.Name, m.Name, m.File+".go"))
				} else {
					errs = append(errs, fmt.Sprintf("%s: rpcs %s and %s generate files %q and %q, one file on a case-insensitive file system - rename one", svc.FullName, prev.Name, m.Name, prev.File+".go", m.File+".go"))
				}
				continue
			}
			byFile[strings.ToLower(m.File)] = m
		}
	}
	errs = append(errs, s.importCycles()...)
	if len(errs) == 0 {
		return nil
	}
	sort.Strings(errs)
	return fmt.Errorf("proto errors:\n  %s", strings.Join(errs, "\n  "))
}
