package golang

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// validateData is the template input for validate.tmpl.
type validateData struct {
	Package string
	Imports []string
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
	if pkg.Name == "" {
		return fmt.Errorf("package has no name")
	}
	if !pkgValidates(pkg) {
		return nil
	}
	r = resolverFor(pkg, r)
	pkgDir := filepath.Join(outDir, pkg.Name)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		return err
	}
	data := buildValidateData(pkg, r)
	formatted, err := renderGo(tmpl("validate.tmpl"), data)
	if err != nil {
		return fmt.Errorf("render validate.go: %w", err)
	}
	return os.WriteFile(filepath.Join(pkgDir, "validate.go"), formatted, 0o644)
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
		if len(errorBodyMembers(ed)) > 0 {
			return true
		}
	}
	return false
}

// errorBodyMembers returns the fields and mixins of ed's `<Name>Body` struct.
func errorBodyMembers(ed *ast.ErrorDecl) []ast.TypeMember {
	var out []ast.TypeMember
	for _, m := range ed.Body {
		switch m.(type) {
		case *ast.Field, *ast.Mixin:
			out = append(out, m)
		}
	}
	return out
}

// buildValidateData renders the Validate() bodies of pkg's types, constrained
// scalars, enums and error bodies, with the imports and regexes they use.
func buildValidateData(pkg *semantic.Package, r *projectResolver) validateData {
	names := sortedKeys(pkg.Types)

	uses := map[string]bool{}
	regexes := newRegexRegistry()
	ctx := emitCtx{pkg: pkg, uses: uses, regexes: regexes, resolver: r}
	var types []validatorType
	for _, name := range names {
		td := pkg.Types[name]
		types = append(types, validatorType{
			Name:        name,
			TypeParams:  td.TypeParams,
			Checks:      collectChecks(td, pkg, r, ctx),
			PtrReceiver: true,
		})
	}

	for _, name := range sortedKeys(pkg.Scalars) {
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
	for _, name := range sortedKeys(pkg.Enums) {
		ed := pkg.Enums[name]
		checks := enumValidateChecks(ed)
		if len(checks) > 0 {
			uses["fmt"] = true
		}
		types = append(types, validatorType{
			Name:        name,
			Checks:      checks,
			PtrReceiver: false,
		})
	}

	for _, name := range sortedKeys(pkg.Errors) {
		ed := pkg.Errors[name]
		body := &ast.TypeDecl{Name: name + "Body", Body: errorBodyMembers(ed)}
		if len(body.Body) == 0 {
			continue
		}
		types = append(types, validatorType{
			Name:        body.Name,
			Checks:      collectChecks(body, pkg, r, ctx),
			PtrReceiver: true,
		})
	}

	imps := sortedKeys(uses)

	return validateData{
		Package:            pkg.Name,
		Imports:            imps,
		RegexVars:          regexes.entries,
		Types:              types,
		NeedsValidateValue: uses["reflect"],
	}
}

// collectChecks returns td's Validate() statements: per field its constraint
// checks, then a type-param probe or nested Validate() call.
func collectChecks(td *ast.TypeDecl, pkg *semantic.Package, r *projectResolver, ctx emitCtx) []string {
	var out []string
	// The struct's own deduped Go names (`UserID`, `UserID_2`).
	levelNames := resolvedGoFieldNames(td.Body)
	fieldIdx := 0
	for _, m := range td.Body {
		switch v := m.(type) {
		case *ast.Field:
			goName := levelNames[fieldIdx]
			fieldIdx++
			out = append(out, fieldChecksWithScalar(v, goName, pkg, ctx)...)
			if isTypeParamRef(v.Type, td.TypeParams) {
				if call := typeParamValidateCall(v, goName, ctx); call != "" {
					out = append(out, call)
				}
				continue
			}
			if nested := nestedValidateCall(v, goName, ctx); nested != "" {
				out = append(out, nested)
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
