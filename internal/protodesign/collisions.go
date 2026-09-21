package protodesign

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// checkCollisions rejects what the file system or the Go compiler would
// otherwise merge or refuse later, naming both sides:
//
//   - two design files in one directory declaring different proto
//     packages: they would share one Go package directory;
//   - two services whose Go names map to one output directory;
//   - two RPCs of one service whose Go names map to one file, where the
//     gen-once scaffold would silently keep the first.
func (s *Set) checkCollisions() error {
	var errs []string
	pkgByDir := map[string]string{}
	fileByDir := map[string]string{}
	for _, name := range s.Names {
		f := s.Plugin.FilesByPath[name]
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
