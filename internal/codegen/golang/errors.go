package golang

import (
	"bytes"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/errcat"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// generateErrors writes outDir/<pkg>/errors.go, an error type with its code
// const and methods per error; a package without errors writes nothing.
func generateErrors(pkg *semantic.Package, outDir string, r *projectResolver) error {
	if len(pkg.Errors) == 0 {
		return nil
	}
	return writeGoSource(filepath.Join(outDir, pkg.Name, "errors.go"), buildErrorsGo(pkg, resolverFor(pkg, r)))
}

// buildErrorsGo returns the unformatted source of pkg's errors.go, errors in
// name order.
func buildErrorsGo(pkg *semantic.Package, r *projectResolver) string {
	imports := newImportSet(r.Module, r, goImport{}, errorsNames)
	// Every error type's MarshalJSON encodes through encoding/json.
	imports.use("encoding/json")
	var decls []string
	for _, name := range slices.Sorted(maps.Keys(pkg.Errors)) {
		decls = append(decls, renderError(pkg, pkg.Errors[name], r, imports))
	}
	parts := []string{"package " + pkg.Name + "\n", imports.decl()}
	return strings.Join(append(parts, decls...), "\n")
}

// errorTemplateData is the errors.tmpl input for one error.
type errorTemplateData struct {
	// Doc heads the error type's doc comment ([docHead]).
	Doc                []string
	TypeName           string
	BodyName           string
	ConstName          string
	CtorName           string
	QuotedCode         string
	QuotedMessage      string
	Category           string
	DSLName            string
	Status             int
	HasBody            bool
	HasJSONMember      bool
	BodyInterior       string
	HasResponseHeaders bool
	Headers            []paramBinding
	Cookies            []paramBinding
}

// renderError renders errors.tmpl for ed, adding the packages it names to imports.
func renderError(pkg *semantic.Package, ed *ast.ErrorDecl, r *projectResolver, imports *importSet) string {
	headers, cookies, needsStrconv := errorResponseBindings(ed, pkg, r)
	if len(headers)+len(cookies) > 0 {
		imports.use("net/http")
	}
	if needsStrconv {
		imports.use("strconv")
	}
	data := errorTemplateData{
		Doc:                docHead(semantic.DescriptionLines(ed.Decorators, ed.Doc)),
		TypeName:           idents.ErrorTypeName(ed.Name),
		BodyName:           idents.ErrorBodyName(ed.Name),
		ConstName:          idents.ErrorCodeName(ed.Name),
		CtorName:           idents.ErrorConstructorName(ed.Name),
		QuotedCode:         strconv.Quote(screamingSnake(ed.Name)),
		QuotedMessage:      strconv.Quote(errcat.Message(ed.Category)),
		Category:           ed.Category,
		DSLName:            ed.Name,
		Status:             errcat.Status(ed.Category),
		BodyInterior:       renderTypeBody(ed.Body, pkg, r, imports),
		HasResponseHeaders: len(headers)+len(cookies) > 0,
		Headers:            headers,
		Cookies:            cookies,
		HasBody:            len(ast.Members(ed.Body)) > 0,
		HasJSONMember:      semantic.ErrorHasJSONMember(ed, r.Resolver),
	}
	var buf bytes.Buffer
	if err := errorsTemplate.Execute(&buf, data); err != nil {
		panic(fmt.Sprintf("codegen: render error %s: %v", ed.Name, err))
	}
	return buf.String()
}

// errorsTemplate is errors.tmpl, parsed once at package init.
var errorsTemplate = tmpl("errors.tmpl")

// errorResponseBindings returns ed's @header and @cookie fields with their write
// statements, and whether any of them needs strconv.
func errorResponseBindings(ed *ast.ErrorDecl, pkg *semantic.Package, r *projectResolver) (headers, cookies []paramBinding, needsStrconv bool) {
	// A field promoted from a mixin is reachable as `e.X`.
	fields := semantic.ResolveFields(&ast.TypeDecl{Body: ed.Body}, "", pkg, r.Resolver, resolvedGoFieldNames)
	return responseBindingsFor(fields, "e", pkg, r)
}

// screamingSnake converts an identifier to SCREAMING_SNAKE_CASE, keeping
// initialisms whole (`DBLockedErr` is `DB_LOCKED_ERR`).
func screamingSnake(s string) string {
	parts := idents.SplitFieldName(s)
	for i, p := range parts {
		parts[i] = strings.ToUpper(p)
	}
	return strings.Join(parts, "_")
}
