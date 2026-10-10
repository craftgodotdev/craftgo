package protodesign

import (
	"fmt"
	"go/token"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"google.golang.org/protobuf/types/pluginpb"
)

// goPackageProblem explains why protogen could not give every proto of req a Go
// package, as protogen derives them: a proto with no Go import path, or two that
// share one under different names. It is err's text when neither holds.
func goPackageProblem(req *pluginpb.CodeGeneratorRequest, names []string, opts Options, err error) string {
	design := map[string]bool{}
	for _, n := range names {
		design[n] = true
	}
	type file struct{ name, pkg string }
	byPath := map[string][]file{}
	var paths []string
	for _, fd := range req.GetProtoFile() {
		name := fd.GetName()
		goPath, pkg := splitGoPackage(fd.GetOptions().GetGoPackage())
		importPath := goPath
		if design[name] && opts.pbEnabled() {
			importPath = pbImportPath(opts, name)
		}
		switch {
		case importPath == "" && design[name]:
			return fmt.Sprintf("%s has no `option go_package`, which every design proto needs while output.pb is \"-\"", name)
		case importPath == "":
			return fmt.Sprintf("%s, read from proto.includes, has no `option go_package` naming the Go package its code lives in - an included proto is imported, not generated", name)
		}
		if pkg == "" {
			if goPath == "" {
				goPath = importPath
			}
			pkg = goSanitized(path.Base(goPath))
		}
		if _, ok := byPath[importPath]; !ok {
			paths = append(paths, importPath)
		}
		byPath[importPath] = append(byPath[importPath], file{name, pkg})
	}
	for _, p := range paths {
		files := byPath[p]
		for _, f := range files[1:] {
			if f.pkg == files[0].pkg {
				continue
			}
			why := "protos with one go_package path are one Go package"
			if design[files[0].name] && design[f.name] && opts.pbEnabled() {
				why = "the protos of one directory are one Go package"
			}
			return fmt.Sprintf("%s names its Go package %s %q but %s names it %q - %s, so give them one name with the `;name` suffix of option go_package",
				files[0].name, p, files[0].pkg, f.name, f.pkg, why)
		}
	}
	return err.Error()
}

// splitGoPackage splits `option go_package` into its import path and the name after `;`.
func splitGoPackage(goPackage string) (importPath, name string) {
	importPath, name, _ = strings.Cut(goPackage, ";")
	return importPath, name
}

// goSanitized turns s into a Go identifier as protogen does when it names a package after its
// import path: every rune but a letter or digit becomes `_`, and a keyword, or a name that does
// not open with a letter, gains a leading `_`.
func goSanitized(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return '_'
	}, s)
	if r, _ := utf8.DecodeRuneInString(s); token.Lookup(s).IsKeyword() || !unicode.IsLetter(r) {
		return "_" + s
	}
	return s
}
