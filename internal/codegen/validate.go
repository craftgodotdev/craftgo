// Validate codegen lives across five files in this package, organised
// by layer rather than by decorator:
//
//   - validate.go          driver — orchestrates Generate / collect / template
//   - validate_registry.go decorator → emit-function dispatch table
//   - validate_emit.go     per-validator emitters + cross-cutting helpers
//   - validate_args.go     decorator-argument extractors (intArg, sizeArg, ...)
//   - validate_types.go    field-shape predicates (isStringOrOptString, ...)
//
// To add a new validator: write its emit function in validate_emit.go,
// register it as one row in `validators` (validate_registry.go). Type
// guards and arg helpers are reusable from validate_types.go /
// validate_args.go — most new validators won't need new ones.

package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/semantic"
)

// validateData is the template input for `validate.tmpl`. It is computed
// up front so the template stays declarative — every conditional is
// resolved in Go code where unit tests can pin behaviour.
type validateData struct {
	Package string
	Imports []string
	Types   []validatorType
}

// validatorType is one Validate() method block in `validate.tmpl`.
// TypeParams is non-empty for generic decls — the template uses it to
// build the receiver suffix `[T any, ...]` so the method is declared on
// the parametric type itself, e.g. `func (v *Page[T]) Validate() error`.
type validatorType struct {
	Name       string
	TypeParams []string
	Checks     []string
}

// GenerateValidators writes `validate.go` next to `types.go`. The file
// adds a `Validate() error` method to every concrete TypeDecl. Types
// without any constraints get an empty stub so handlers can call
// `req.Validate()` uniformly.
func GenerateValidators(pkg *semantic.Package, outDir string) error {
	if pkg.Name == "" {
		return fmt.Errorf("package has no name")
	}
	pkgDir := filepath.Join(outDir, pkg.Name)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		return err
	}
	data := buildValidateData(pkg)
	formatted, err := renderGo(tmpl("validate.tmpl"), data)
	if err != nil {
		return fmt.Errorf("render validate.go: %w", err)
	}
	return os.WriteFile(filepath.Join(pkgDir, "validate.go"), formatted, 0o644)
}

// buildValidateData walks every TypeDecl, builds the per-field check
// list, and folds the resulting imports into a single sorted set. Both
// concrete and generic decls produce a Validate(); generics emit with a
// parametric receiver (see [validatorType.TypeParams]).
func buildValidateData(pkg *semantic.Package) validateData {
	names := make([]string, 0, len(pkg.Types))
	for n := range pkg.Types {
		names = append(names, n)
	}
	sort.Strings(names)

	uses := map[string]bool{}
	var types []validatorType
	for _, name := range names {
		td := pkg.Types[name]
		types = append(types, validatorType{
			Name:       name,
			TypeParams: td.TypeParams,
			Checks:     collectChecks(td, pkg, uses),
		})
	}

	imps := make([]string, 0, len(uses))
	for k := range uses {
		imps = append(imps, k)
	}
	sort.Strings(imps)

	return validateData{
		Package: pkg.Name,
		Imports: imps,
		Types:   types,
	}
}

// collectChecks returns every Go statement that should land inside a
// type's Validate() body. Empty result means the type compiles into an
// `if-less` Validate() that just returns nil.
//
// Per-field, the order of checks is:
//
//  1. Decorator-driven validators (registry dispatch in validate_registry.go).
//  2. Generic type-parameter fields → runtime type-assertion path.
//  3. Enum-typed fields → auto switch-case validity check.
//  4. User-defined struct fields → recursive `field.Validate()` call.
//
// Steps 2-4 are mutually exclusive: a field is either a typeParam ref,
// an enum, a struct, or a primitive. Primitives reach none of them.
func collectChecks(td *ast.TypeDecl, pkg *semantic.Package, uses map[string]bool) []string {
	var out []string
	for _, m := range td.Body {
		f, ok := m.(*ast.Field)
		if !ok {
			continue
		}
		out = append(out, fieldChecksWithPkg(f, pkg, uses)...)
		if isTypeParamRef(f.Type, td.TypeParams) {
			if call := typeParamValidateCall(f); call != "" {
				out = append(out, call)
			}
			continue
		}
		if call := enumValueCheck(f, pkg, uses); call != "" {
			out = append(out, call)
		}
		if nested := nestedValidateCall(f, pkg, uses); nested != "" {
			out = append(out, nested)
		}
	}
	return out
}
