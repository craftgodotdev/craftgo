package golang

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// validateData is the template input for validate.tmpl.
type validateData struct {
	Package    string
	ImportDecl string
	// RegexVars are the package-level compiled regexes, one per distinct pattern.
	RegexVars []regexVar
	Types     []validatorType
	// NeedsValidateValue emits the reflective validateValue helper that a
	// type-parameter probe falls back to for a composite argument.
	NeedsValidateValue bool
}

// regexVar binds a pattern to its package-level Go identifier.
type regexVar struct {
	Name    string
	Pattern string
}

// validatorType is one Validate() method; TypeParams make its receiver parametric.
type validatorType struct {
	Name       string
	TypeParams []string
	Checks     []string
	// PtrReceiver is false for a scalar or enum: a value receiver can convert
	// `v` to its primitive and is callable on a map-range copy.
	PtrReceiver bool
}

// generateValidators writes outDir/<pkg>/validate.go, where every type gets a
// Validate() even when empty; a package with nothing to validate writes none.
func generateValidators(pkg *semantic.Package, outDir string, r *projectResolver) error {
	if !pkgValidates(pkg) {
		return nil
	}
	return writeGo(filepath.Join(outDir, pkg.Name, "validate.go"), tmpl("validate.tmpl"), buildValidateData(pkg, resolverFor(pkg, r)))
}

// pkgValidates reports whether pkg declares anything with a generated Validate().
func pkgValidates(pkg *semantic.Package) bool {
	if len(pkg.Types) > 0 || len(pkg.Enums) > 0 {
		return true
	}
	for _, sd := range pkg.Scalars {
		if scalarDeclHasValidators(sd) {
			return true
		}
	}
	for _, ed := range pkg.Errors {
		if len(ast.Members(ed.Body)) > 0 {
			return true
		}
	}
	return false
}

// buildValidateData renders the Validate() bodies of pkg's types, constrained
// scalars, enums and error bodies, with the imports and regexes they use.
func buildValidateData(pkg *semantic.Package, r *projectResolver) validateData {
	names := slices.Sorted(maps.Keys(pkg.Types))

	imports := newImportSet(r.Module, r, goImport{}, validateNames)
	regexes := newRegexRegistry()
	ctx := emitCtx{pkg: pkg, imports: imports, regexes: regexes, resolver: r, autoBound: autoBindings(r.Project())}
	var types []validatorType
	for _, name := range names {
		td := pkg.Types[name]
		types = append(types, validatorType{
			Name:        name,
			TypeParams:  td.TypeParams,
			Checks:      collectChecks(td, ctx),
			PtrReceiver: true,
		})
	}

	for _, name := range slices.Sorted(maps.Keys(pkg.Scalars)) {
		sd := pkg.Scalars[name]
		if !scalarDeclHasValidators(sd) {
			continue
		}
		types = append(types, validatorType{
			Name:        name,
			Checks:      scalarValidateChecks(sd, ctx),
			PtrReceiver: false,
		})
	}
	for _, name := range slices.Sorted(maps.Keys(pkg.Enums)) {
		types = append(types, validatorType{
			Name:        name,
			Checks:      enumValidateChecks(pkg.Enums[name], ctx),
			PtrReceiver: false,
		})
	}

	for _, name := range slices.Sorted(maps.Keys(pkg.Errors)) {
		ed := pkg.Errors[name]
		body := &ast.TypeDecl{Name: idents.ErrorBodyName(name), Body: ast.Members(ed.Body)}
		if len(body.Body) == 0 {
			continue
		}
		types = append(types, validatorType{
			Name:        body.Name,
			Checks:      collectChecks(body, ctx),
			PtrReceiver: true,
		})
	}

	return validateData{
		Package:            pkg.Name,
		ImportDecl:         imports.decl(),
		RegexVars:          regexes.entries,
		Types:              types,
		NeedsValidateValue: imports.has("reflect"),
	}
}

// collectChecks returns td's Validate() statements: per field its constraint
// checks, then the Validate() calls and type-parameter probes its value needs.
func collectChecks(td *ast.TypeDecl, ctx emitCtx) []string {
	var out []string
	// The struct's own deduped Go names (`UserID`, `UserID_2`).
	levelNames := resolvedGoFieldNames(td.Body)
	fieldIdx := 0
	for _, m := range td.Body {
		switch v := m.(type) {
		case *ast.Field:
			rf := semantic.ResolveField(v, ctx.pkg, ctx.resolver.Project())
			t := fieldTarget(rf, "v."+levelNames[fieldIdx], ctx.subject(v))
			fieldIdx++
			out = append(out, fieldChecks(rf, t, ctx)...)
			if calls := validateCalls(t, td.TypeParams, ctx); calls != "" {
				out = append(out, calls)
			}
		case *ast.Mixin:
			if call := mixinValidateCall(v); call != "" {
				out = append(out, call)
			}
		}
	}
	// Cross-field checks run last, so a malformed field reports its own error first.
	out = append(out, crossFieldChecks(td, ctx)...)
	return out
}

// mixinValidateCall calls an embedded mixin's Validate() through its embedded
// name, the ref's last segment (`shared.Audit` embeds as `Audit`).
func mixinValidateCall(m *ast.Mixin) string {
	if m == nil || m.Ref == nil || m.Ref.Name == nil || len(m.Ref.Name.Parts) == 0 {
		return ""
	}
	last := m.Ref.Name.Parts[len(m.Ref.Name.Parts)-1]
	return fmt.Sprintf("if err := v.%s.Validate(); err != nil {\nreturn err\n}", last)
}
