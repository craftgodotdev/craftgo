package codegen

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// categoryStatus maps each reserved error category to its HTTP status code.
// Mirrors the table in the project README; new categories added there must
// be added here as well.
var categoryStatus = map[string]int{
	"BadRequest":           400,
	"Unauthorized":         401,
	"PaymentRequired":      402,
	"Forbidden":            403,
	"NotFound":             404,
	"MethodNotAllowed":     405,
	"NotAcceptable":        406,
	"Conflict":             409,
	"Gone":                 410,
	"LengthRequired":       411,
	"PreconditionFailed":   412,
	"PayloadTooLarge":      413,
	"UnsupportedMediaType": 415,
	"UnprocessableEntity":  422,
	"Locked":               423,
	"TooManyRequests":      429,
	"Internal":             500,
	"NotImplemented":       501,
	"BadGateway":           502,
	"ServiceUnavailable":   503,
	"GatewayTimeout":       504,
}

// categoryMessage maps each reserved error category to its default
// human-readable message (used as the runtime default for `Message`).
var categoryMessage = map[string]string{
	"BadRequest":           "Bad request",
	"Unauthorized":         "Unauthorized",
	"PaymentRequired":      "Payment required",
	"Forbidden":            "Forbidden",
	"NotFound":             "Not found",
	"MethodNotAllowed":     "Method not allowed",
	"NotAcceptable":        "Not acceptable",
	"Conflict":             "Conflict",
	"Gone":                 "Resource gone",
	"LengthRequired":       "Length required",
	"PreconditionFailed":   "Precondition failed",
	"PayloadTooLarge":      "Payload too large",
	"UnsupportedMediaType": "Unsupported media type",
	"UnprocessableEntity":  "Unprocessable entity",
	"Locked":               "Resource locked",
	"TooManyRequests":      "Too many requests",
	"Internal":             "Internal server error",
	"NotImplemented":       "Not implemented",
	"BadGateway":           "Bad gateway",
	"ServiceUnavailable":   "Service unavailable",
	"GatewayTimeout":       "Gateway timeout",
}

// GenerateErrors emits a single `errors.go` file under outDir/<pkg.Name>/
// declaring one struct + constructor + Error()/HTTPStatus() methods +
// SCREAMING_SNAKE error-code constant for every [ast.ErrorDecl] in pkg.
// When pkg has no errors the function is a no-op.
//
// Equivalent to [GenerateErrorsPackage] with a nil resolver; kept for
// single-package callers that don't reach across packages.
func GenerateErrors(pkg *semantic.Package, outDir string) error {
	return GenerateErrorsPackage(pkg, outDir, nil)
}

// GenerateErrorsPackage is the multi-package variant of [GenerateErrors].
// The [ProjectResolver] supplies the cross-package import paths for body
// fields (e.g. an error in `tasks` whose body carries a `users.UserRef`)
// AND the cross-package scalar / enum resolution needed to format a
// non-string `@header` / `@cookie` error field (`cost shared.Cents`).
// A nil resolver falls back to local-only resolution.
func GenerateErrorsPackage(pkg *semantic.Package, outDir string, r *ProjectResolver) error {
	if pkg.Name == "" {
		return fmt.Errorf("package has no name")
	}
	if len(pkg.Errors) == 0 {
		return nil
	}
	pkgDir := filepath.Join(outDir, pkg.Name)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		return err
	}
	src := buildErrorsGo(pkg, r)
	formatted, err := format.Source([]byte(src))
	if err != nil {
		return fmt.Errorf("format errors.go: %w\n--- source ---\n%s", err, src)
	}
	return os.WriteFile(filepath.Join(pkgDir, "errors.go"), formatted, 0o644)
}

// buildErrorsGo assembles errors.go in alphabetical name order. The
// import block is built from two sources: `net/http` lands when any
// error declares `@header` / `@cookie` response bindings (the
// generated `WriteResponseHeaders` method needs `http.ResponseWriter`
// and `http.Cookie`); cross-package types referenced by body fields
// surface their `<module>/<typesDir>/<pkg>` Go import paths via the
// shared [collectImports] machinery. The result is returned
// pre-formatting; the caller runs `go/format` to normalise whitespace.
func buildErrorsGo(pkg *semantic.Package, r *ProjectResolver) string {
	crossPkg := r.crossPkgMap()
	names := make([]string, 0, len(pkg.Errors))
	for n := range pkg.Errors {
		names = append(names, n)
	}
	sort.Strings(names)

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

	imports := map[string]bool{}
	if needsHTTP {
		imports["net/http"] = true
	}
	if needsStrconv {
		// Non-string @header / @cookie error fields format their value
		// via strconv before writing it to the wire.
		imports["strconv"] = true
	}
	for _, name := range names {
		for _, m := range pkg.Errors[name].Body {
			f, ok := m.(*ast.Field)
			if !ok {
				continue
			}
			collectFieldImports(f.Type, imports)
			walkCrossPkgImports(f.Type, crossPkg, imports)
		}
	}

	parts := []string{
		"// Code generated by craftgo. DO NOT EDIT.\n",
		"package " + pkg.Name + "\n",
	}
	if len(imports) > 0 {
		paths := make([]string, 0, len(imports))
		for p := range imports {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		parts = append(parts, renderImports(paths))
	}
	for _, name := range names {
		parts = append(parts, renderError(pkg, pkg.Errors[name], r))
	}
	return strings.Join(parts, "\n")
}

// errorBodyField is the per-field record passed into errors.tmpl. Each
// entry renders one line inside the generated `<Name>Body` struct.
type errorBodyField struct {
	GoName  string
	Type    string
	JSONTag string
}

// errorBinding holds the fully-rendered Go statement that writes one
// `@header` / `@cookie` error field onto the response (header/cookie
// name + value formatting already baked in). The template drops Stmt
// verbatim inside WriteResponseHeaders.
type errorBinding struct {
	Stmt string
}

// errorTemplateData is the full payload handed to errors.tmpl per error.
// Field naming mirrors the template placeholders so the mapping stays
// obvious at the call site.
type errorTemplateData struct {
	TypeName           string
	BodyName           string
	ConstName          string
	QuotedCode         string
	QuotedMessage      string
	Category           string
	DSLName            string
	Status             int
	HasBody            bool
	BodyFields         []errorBodyField
	HasResponseHeaders bool
	Headers            []errorBinding
	Cookies            []errorBinding
}

// renderError executes errors.tmpl for one [ast.ErrorDecl]. The
// template emits, in order: the SCREAMING_SNAKE error-code const, the
// (optional) body struct, the typed error struct with its unexported
// code / message metadata, the constructor, Error() / ErrCode() /
// HTTPStatus() methods, and the optional WriteResponseHeaders method
// when the error declares any `@header` / `@cookie` fields. The
// constructor takes a body-struct argument iff the DSL declares ≥1
// custom field.
func renderError(pkg *semantic.Package, ed *ast.ErrorDecl, r *ProjectResolver) string {
	headers, cookies, _ := errorResponseBindings(ed, pkg, r)
	data := errorTemplateData{
		TypeName:           errSuffix(ed.Name),
		BodyName:           ed.Name + "Body",
		ConstName:          "ErrCode" + ed.Name,
		QuotedCode:         strconv.Quote(screamingSnake(ed.Name)),
		QuotedMessage:      strconv.Quote(categoryMessage[ed.Category]),
		Category:           ed.Category,
		DSLName:            ed.Name,
		Status:             categoryStatus[ed.Category],
		BodyFields:         buildErrorBodyFields(errorCustomFields(ed)),
		HasResponseHeaders: len(headers)+len(cookies) > 0,
		Headers:            toErrorBindings(headers),
		Cookies:            toErrorBindings(cookies),
	}
	data.HasBody = len(data.BodyFields) > 0
	var buf bytes.Buffer
	if err := errorsTemplate.Execute(&buf, data); err != nil {
		panic(fmt.Sprintf("codegen: render error %s: %v", ed.Name, err))
	}
	return buf.String()
}

// errorsTemplate is parsed once and reused for every error decl. The
// tmpl helper panics on parse failure so a malformed template fails the
// process at the first generation attempt.
var errorsTemplate = tmpl("errors.tmpl")

// buildErrorBodyFields turns each declared body field into the
// template-friendly shape: PascalCase Go name, rendered Go type, and
// the JSON tag string (response-bound fields are tagged `"-"` so the
// value rides on a response header instead of the body).
func buildErrorBodyFields(fields []*ast.Field) []errorBodyField {
	out := make([]errorBodyField, len(fields))
	for i, f := range fields {
		tag := strconv.Quote(f.Name)
		if isResponseBoundField(f) {
			tag = `"-"`
		}
		out[i] = errorBodyField{
			GoName:  GoFieldName(f.Name),
			Type:    GoTypeRef(f.Type),
			JSONTag: tag,
		}
	}
	return out
}

// toErrorBindings adapts the shared paramBinding shape into the
// template's view: each binding's pre-rendered write statement lives in
// [paramBinding.Bind].
func toErrorBindings(in []paramBinding) []errorBinding {
	out := make([]errorBinding, len(in))
	for i, b := range in {
		out[i] = errorBinding{Stmt: b.Bind}
	}
	return out
}

// errorCustomFields returns every Field in the error body. `code` and
// `message` are NOT special-cased - they coexist with the framework's
// unexported `code` / `message` metadata fields by virtue of Go's
// case-sensitive identifiers (DSL `code` → exported Go `Code`,
// distinct from the unexported framework field).
func errorCustomFields(ed *ast.ErrorDecl) []*ast.Field {
	var out []*ast.Field
	for _, m := range ed.Body {
		f, ok := m.(*ast.Field)
		if !ok {
			continue
		}
		out = append(out, f)
	}
	return out
}

// errorResponseBindings walks the error body and returns the
// `@header` / `@cookie` fields whose value is written onto the response
// writer instead of the JSON body. Each entry's [paramBinding.Bind]
// holds the fully-rendered write statement (value formatting included);
// needsStrconv is true when any non-string field needs the strconv
// import. The resolver `r` resolves cross-package scalars / enums to
// their wire primitive so `cost shared.Cents @header` formats the same
// as on the success-response path.
func errorResponseBindings(ed *ast.ErrorDecl, pkg *semantic.Package, r *ProjectResolver) (headers, cookies []paramBinding, needsStrconv bool) {
	for _, m := range ed.Body {
		f, ok := m.(*ast.Field)
		if !ok {
			continue
		}
		if f.Name == "code" || f.Name == "message" {
			continue
		}
		kind := bindingFromDecorators(f.Decorators)
		if kind != "header" && kind != "cookie" {
			continue
		}
		stmt, ns := renderResponseWrite(f, pkg, r, kind, "e")
		if ns {
			needsStrconv = true
		}
		entry := paramBinding{Bind: stmt}
		switch kind {
		case "header":
			headers = append(headers, entry)
		case "cookie":
			cookies = append(cookies, entry)
		}
	}
	return headers, cookies, needsStrconv
}

// isResponseBoundField reports whether f carries `@header` or `@cookie`
// - used to mark the JSON tag as `"-"` so the field does not double
// up in the body alongside the response-header / cookie write.
func isResponseBoundField(f *ast.Field) bool {
	switch bindingFromDecorators(f.Decorators) {
	case "header", "cookie":
		return true
	}
	return false
}

// errSuffix appends `Err` to name unless name already ends in `Err` or
// `Error` - the smart-suffix rule documented in the README.
func errSuffix(name string) string {
	if strings.HasSuffix(name, "Err") || strings.HasSuffix(name, "Error") {
		return name
	}
	return name + "Err"
}

// screamingSnake converts a PascalCase / camelCase identifier to
// SCREAMING_SNAKE_CASE for use as a default error-code constant. Common
// initialisms ("HTTP", "ID") collapse to their upper form, e.g.
// `UserNotFound` → `USER_NOT_FOUND` and `DBLockedErr` → `DB_LOCKED_ERR`.
func screamingSnake(s string) string {
	parts := idents.SplitFieldName(s)
	for i, p := range parts {
		parts[i] = strings.ToUpper(p)
	}
	return strings.Join(parts, "_")
}
