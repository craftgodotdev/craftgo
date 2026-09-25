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
	names := slices.Sorted(maps.Keys(pkg.Errors))

	needsHTTP := false
	needsStrconv := false
	for _, name := range names {
		hs, cs, ns := errorResponseBindings(pkg.Errors[name], pkg, r)
		if len(hs)+len(cs) > 0 {
			needsHTTP = true
		}
		if ns {
			needsStrconv = true
		}
	}

	// Every error type's MarshalJSON encodes through encoding/json.
	imports := map[string]bool{"encoding/json": true}
	if needsHTTP {
		imports["net/http"] = true
	}
	if needsStrconv {
		imports["strconv"] = true
	}
	for _, name := range names {
		collectBodyImports(pkg.Errors[name].Body, pkg, r, imports)
	}

	parts := []string{"package " + pkg.Name + "\n"}
	if len(imports) > 0 {
		parts = append(parts, renderImports(slices.Sorted(maps.Keys(imports))))
	}
	for _, name := range names {
		parts = append(parts, renderError(pkg, pkg.Errors[name], r))
	}
	return strings.Join(parts, "\n")
}

// errorTemplateData is the errors.tmpl input for one error.
type errorTemplateData struct {
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
	BodyInterior       string
	HasResponseHeaders bool
	Headers            []paramBinding
	Cookies            []paramBinding
}

// renderError renders errors.tmpl for ed.
func renderError(pkg *semantic.Package, ed *ast.ErrorDecl, r *projectResolver) string {
	headers, cookies, _ := errorResponseBindings(ed, pkg, r)
	data := errorTemplateData{
		TypeName:           idents.ErrorTypeName(ed.Name),
		BodyName:           idents.ErrorBodyName(ed.Name),
		ConstName:          idents.ErrorCodeName(ed.Name),
		CtorName:           idents.ErrorConstructorName(ed.Name),
		QuotedCode:         strconv.Quote(screamingSnake(ed.Name)),
		QuotedMessage:      strconv.Quote(errcat.Message(ed.Category)),
		Category:           ed.Category,
		DSLName:            ed.Name,
		Status:             errcat.Status(ed.Category),
		BodyInterior:       renderTypeBody(ed.Body, pkg, r),
		HasResponseHeaders: len(headers)+len(cookies) > 0,
		Headers:            headers,
		Cookies:            cookies,
		HasBody:            len(ast.Members(ed.Body)) > 0,
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
	return responseBindingsFor(&ast.TypeDecl{Body: ed.Body}, "", "e", pkg, r)
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
