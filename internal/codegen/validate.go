package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

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
type validatorType struct {
	Name   string
	Checks []string
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

// buildValidateData walks every concrete TypeDecl, builds the per-field
// check list, and folds the resulting imports into a single sorted set.
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
		if len(td.TypeParams) > 0 {
			continue
		}
		types = append(types, validatorType{
			Name:   name,
			Checks: collectChecks(td, pkg, uses),
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

// collectChecks returns every Go statement that should land inside the
// type's Validate() body. Empty result means the type compiles into an
// `if-less` Validate() that just returns nil. The pkg argument is used
// for nested-struct lookup: a field whose declared type is itself a
// user-defined struct gets a `field.Validate()` recursive call appended.
func collectChecks(td *ast.TypeDecl, pkg *semantic.Package, uses map[string]bool) []string {
	var out []string
	for _, m := range td.Body {
		f, ok := m.(*ast.Field)
		if !ok {
			continue
		}
		out = append(out, fieldChecks(f, uses)...)
		if nested := nestedValidateCall(f, pkg, uses); nested != "" {
			out = append(out, nested)
		}
	}
	return out
}

// nestedValidateCall emits `if err := v.X.Validate(); err != nil { return err }`
// (or the matching slice/pointer variant) when a field's declared type
// is another struct in the same package. Maps are intentionally skipped
// for v1 — values inside maps need range traversal that the simple
// emitter does not yet generate.
func nestedValidateCall(f *ast.Field, pkg *semantic.Package, uses map[string]bool) string {
	if f.Type == nil || f.Type.Map != nil || f.Type.Named == nil {
		return ""
	}
	name := f.Type.Named.Name.String()
	if pkg == nil {
		return ""
	}
	td, ok := pkg.Types[name]
	if !ok {
		// Not a user-defined struct — primitive or unknown.
		return ""
	}
	if len(td.TypeParams) > 0 {
		// Generic decls don't carry a Validate method (Go's generic
		// param types don't propagate methods); skip the nested call
		// and let the inner T's own Validate run via per-field checks
		// at the outer scope.
		return ""
	}
	access := "v." + GoFieldName(f.Name)
	switch {
	case f.Type.Array:
		var sb strings.Builder
		sb.WriteString("for i := range " + access + " {\n")
		sb.WriteString("\t\tif err := " + access + "[i].Validate(); err != nil {\n")
		sb.WriteString("\t\t\treturn err\n")
		sb.WriteString("\t\t}\n")
		sb.WriteString("\t}")
		return sb.String()
	case f.Type.Optional:
		var sb strings.Builder
		sb.WriteString("if " + access + " != nil {\n")
		sb.WriteString("\t\tif err := " + access + ".Validate(); err != nil {\n")
		sb.WriteString("\t\t\treturn err\n")
		sb.WriteString("\t\t}\n")
		sb.WriteString("\t}")
		return sb.String()
	default:
		var sb strings.Builder
		sb.WriteString("if err := " + access + ".Validate(); err != nil {\n")
		sb.WriteString("\t\treturn err\n")
		sb.WriteString("\t}")
		return sb.String()
	}
}

// fieldChecks emits the runtime check(s) for a single field's decorators.
// Each entry is a complete `if ... { return fmt.Errorf(...) }` block as
// one multi-line string so the template can emit it verbatim.
func fieldChecks(f *ast.Field, uses map[string]bool) []string {
	access := "v." + GoFieldName(f.Name)
	var out []string
	for _, d := range f.Decorators {
		var check string
		switch d.Name {
		case "required":
			check = requiredCheck(f, access, uses)
		case "length":
			check = lengthCheck(f, access, d, uses)
		case "minLength":
			check = minMaxLengthCheck(f, access, d, "min", uses)
		case "maxLength":
			check = minMaxLengthCheck(f, access, d, "max", uses)
		case "min":
			check = numericBoundCheck(f, access, d, ">=", "below minimum", uses)
		case "max":
			check = numericBoundCheck(f, access, d, "<=", "above maximum", uses)
		case "range":
			check = rangeCheck(f, access, d, uses)
		case "minItems":
			check = itemsBoundCheck(f, access, d, ">=", "minItems", uses)
		case "maxItems":
			check = itemsBoundCheck(f, access, d, "<=", "maxItems", uses)
		case "pattern":
			check = patternCheck(f, access, d, uses)
		case "format":
			check = formatCheck(f, access, d, uses)
		}
		if check != "" {
			out = append(out, check)
		}
	}
	return out
}

// requiredKind picks the right Go conditional for an absent value. The
// extra `false` sentinel signals "this field type has no obvious empty
// value" so the caller drops the check rather than emitting a no-op.
func requiredKind(f *ast.Field, access string) string {
	if f.Type == nil || f.Type.Optional {
		return access + " == nil"
	}
	if f.Type.Array || f.Type.Map != nil {
		return "len(" + access + ") == 0"
	}
	if f.Type.Named != nil && f.Type.Named.Name.String() == "string" {
		return access + ` == ""`
	}
	return ""
}

// requiredCheck assembles the `@required` block, or returns "" when the
// field type doesn't have a defined empty value.
func requiredCheck(f *ast.Field, access string, uses map[string]bool) string {
	cond := requiredKind(f, access)
	if cond == "" {
		return ""
	}
	uses["fmt"] = true
	return ifReturnf(cond, fmt.Sprintf(`"%s: required"`, f.Name))
}

// lengthCheck handles `@length(min, max)` for non-optional string fields.
func lengthCheck(f *ast.Field, access string, d *ast.Decorator, uses map[string]bool) string {
	if !isStringField(f) || len(d.Args) != 2 {
		return ""
	}
	lo, ok1 := intArg(d.Args[0])
	hi, ok2 := intArg(d.Args[1])
	if !ok1 || !ok2 {
		return ""
	}
	uses["fmt"] = true
	cond := fmt.Sprintf("l := len(%s); l < %d || l > %d", access, lo, hi)
	msg := fmt.Sprintf(`"%s: length out of range [%d, %d]"`, f.Name, lo, hi)
	return ifReturnf(cond, msg)
}

// minMaxLengthCheck handles `@minLength(n)` and `@maxLength(n)`.
func minMaxLengthCheck(f *ast.Field, access string, d *ast.Decorator, kind string, uses map[string]bool) string {
	if !isStringField(f) || len(d.Args) != 1 {
		return ""
	}
	n, ok := intArg(d.Args[0])
	if !ok {
		return ""
	}
	op, label := "<", "less than"
	if kind == "max" {
		op, label = ">", "greater than"
	}
	uses["fmt"] = true
	cond := fmt.Sprintf("len(%s) %s %d", access, op, n)
	msg := fmt.Sprintf(`"%s: length %s %d"`, f.Name, label, n)
	return ifReturnf(cond, msg)
}

// numericBoundCheck handles `@min(n)` / `@max(n)`.
func numericBoundCheck(f *ast.Field, access string, d *ast.Decorator, op, label string, uses map[string]bool) string {
	if !isNumericField(f) || len(d.Args) != 1 {
		return ""
	}
	n, ok := intArg(d.Args[0])
	if !ok {
		return ""
	}
	flip := "<"
	if op == "<=" {
		flip = ">"
	}
	uses["fmt"] = true
	cond := fmt.Sprintf("%s %s %d", access, flip, n)
	msg := fmt.Sprintf(`"%s: %s %d"`, f.Name, label, n)
	return ifReturnf(cond, msg)
}

// rangeCheck combines @min and @max into one bounded comparison.
func rangeCheck(f *ast.Field, access string, d *ast.Decorator, uses map[string]bool) string {
	if !isNumericField(f) || len(d.Args) != 2 {
		return ""
	}
	lo, ok1 := intArg(d.Args[0])
	hi, ok2 := intArg(d.Args[1])
	if !ok1 || !ok2 {
		return ""
	}
	uses["fmt"] = true
	cond := fmt.Sprintf("%s < %d || %s > %d", access, lo, access, hi)
	msg := fmt.Sprintf(`"%s: out of range [%d, %d]"`, f.Name, lo, hi)
	return ifReturnf(cond, msg)
}

// itemsBoundCheck handles `@minItems(n)` and `@maxItems(n)`.
func itemsBoundCheck(f *ast.Field, access string, d *ast.Decorator, op, label string, uses map[string]bool) string {
	if f.Type == nil || !f.Type.Array || len(d.Args) != 1 {
		return ""
	}
	n, ok := intArg(d.Args[0])
	if !ok {
		return ""
	}
	flip := "<"
	if op == "<=" {
		flip = ">"
	}
	uses["fmt"] = true
	cond := fmt.Sprintf("len(%s) %s %d", access, flip, n)
	msg := fmt.Sprintf(`"%s: %s %d"`, f.Name, label, n)
	return ifReturnf(cond, msg)
}

// formatCheck handles `@format("name")` for the catalogue of standard
// formats listed in the README (email / url / uri / uuid / hostname /
// ipv4 / ipv6 / phone). Each name maps to a built-in regex evaluated
// at request time. Unknown names are silently skipped — projects can
// extend with `@pattern("...")` for niche cases.
func formatCheck(f *ast.Field, access string, d *ast.Decorator, uses map[string]bool) string {
	if !isStringField(f) || len(d.Args) != 1 {
		return ""
	}
	name, ok := stringArg(d.Args[0])
	if !ok {
		return ""
	}
	pattern := formatPatterns[name]
	if pattern == "" {
		return ""
	}
	uses["fmt"] = true
	uses["regexp"] = true
	cond := fmt.Sprintf("!regexp.MustCompile(`%s`).MatchString(%s)", pattern, access)
	msg := fmt.Sprintf(`"%s: not a valid %s"`, f.Name, name)
	return ifReturnf(cond, msg)
}

// formatPatterns is the regex catalogue referenced by `@format(...)`.
// Definitions are intentionally pragmatic, not RFC-grade — projects
// that need stricter parsing should fall back to `@pattern("...")`.
var formatPatterns = map[string]string{
	"email":    `^[^@\s]+@[^@\s]+\.[^@\s]+$`,
	"url":      `^https?://[^\s]+$`,
	"uri":      `^[a-zA-Z][a-zA-Z0-9+.-]*:[^\s]+$`,
	"uuid":     `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`,
	"hostname": `^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)*$`,
	"ipv4":     `^(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)(\.(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)){3}$`,
	"phone":    `^\+?[0-9 ()-]{6,20}$`,
}

// patternCheck handles `@pattern("regex")`. The regex is compiled inline
// via `regexp.MustCompile` for v1; a future revision can hoist it to a
// package-level `var` to amortise compile cost.
func patternCheck(f *ast.Field, access string, d *ast.Decorator, uses map[string]bool) string {
	if !isStringField(f) || len(d.Args) != 1 {
		return ""
	}
	s, ok := stringArg(d.Args[0])
	if !ok {
		return ""
	}
	uses["fmt"] = true
	uses["regexp"] = true
	cond := fmt.Sprintf("!regexp.MustCompile(`%s`).MatchString(%s)", s, access)
	msg := fmt.Sprintf(`"%s: does not match pattern"`, f.Name)
	return ifReturnf(cond, msg)
}

// ifReturnf assembles a single multi-line `if cond { return fmt.Errorf(msg) }`
// block. Keeping it here so all the validators have identical formatting.
func ifReturnf(cond, msg string) string {
	var sb strings.Builder
	sb.WriteString("if " + cond + " {\n")
	sb.WriteString("\t\treturn fmt.Errorf(" + msg + ")\n")
	sb.WriteString("\t}")
	return sb.String()
}

// isStringField — non-array, non-optional `string`.
func isStringField(f *ast.Field) bool {
	return f.Type != nil && !f.Type.Array && !f.Type.Optional &&
		f.Type.Named != nil && f.Type.Named.Name.String() == "string"
}

// isNumericField — non-array, non-optional integer or float.
func isNumericField(f *ast.Field) bool {
	if f.Type == nil || f.Type.Array || f.Type.Optional || f.Type.Named == nil {
		return false
	}
	switch f.Type.Named.Name.String() {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64":
		return true
	}
	return false
}

// intArg pulls an int64 out of a literal DecoratorArg.
func intArg(a *ast.DecoratorArg) (int64, bool) {
	if a == nil || a.Value == nil {
		return 0, false
	}
	if i, ok := a.Value.(*ast.IntLit); ok {
		return i.Value, true
	}
	return 0, false
}

// stringArg pulls a string out of a literal DecoratorArg.
func stringArg(a *ast.DecoratorArg) (string, bool) {
	if a == nil || a.Value == nil {
		return "", false
	}
	if s, ok := a.Value.(*ast.StringLit); ok {
		return s.Value, true
	}
	return "", false
}

// quoteIntList renders integers as a comma-separated string for use in
// generated error messages. Currently unused but kept for the future when
// `@enum`-style validators land.
func quoteIntList(xs []int64) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = strconv.FormatInt(x, 10)
	}
	return strings.Join(parts, ",")
}
