package codegen

import (
	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/semantic"
)

// This file holds the decorator → emit-function dispatch table. The
// rest of the validate codegen is procedural; everything that decides
// "which decorator triggers which Go code" lives here so adding a new
// validator is one entry edit, not three (case label + impl + helper).

// emitCtx is the side-channel state passed to every validator's emit
// function. `uses` collects standard-library imports that the generated
// Validate file needs (`fmt`, `regexp`, ...); `pkg` is forwarded for
// validators that need the symbol table (today only the enum-aware
// @required path).
type emitCtx struct {
	pkg  *semantic.Package
	uses map[string]bool
}

// validatorEntry binds a decorator name to its emit function. The emit
// signature is uniform so the dispatcher in [fieldChecksWithPkg] can
// stay table-driven: every validator returns the Go source for one
// check, or "" to opt out (type mismatch, missing args, etc.).
type validatorEntry struct {
	name string
	emit func(f *ast.Field, access string, d *ast.Decorator, ctx emitCtx) string
}

// validators is the source-of-truth registry. Order doesn't matter for
// correctness — names are looked up — but the table is grouped by
// concern to make scanning easier: presence/strings/numerics/arrays/files.
var validators = []validatorEntry{
	// presence
	{"required", func(f *ast.Field, a string, _ *ast.Decorator, c emitCtx) string {
		return requiredCheckEnumAware(f, a, c.pkg, c.uses)
	}},

	// string
	{"length", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string { return lengthCheck(f, a, d, c.uses) }},
	{"minLength", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return minMaxLengthCheck(f, a, d, "min", c.uses)
	}},
	{"maxLength", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return minMaxLengthCheck(f, a, d, "max", c.uses)
	}},
	{"pattern", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string { return patternCheck(f, a, d, c.uses) }},
	{"format", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string { return formatCheck(f, a, d, c.uses) }},

	// numeric
	{"min", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return numericBoundCheck(f, a, d, ">=", "below minimum", c.uses)
	}},
	{"max", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return numericBoundCheck(f, a, d, "<=", "above maximum", c.uses)
	}},
	{"range", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string { return rangeCheck(f, a, d, c.uses) }},
	{"positive", func(f *ast.Field, a string, _ *ast.Decorator, c emitCtx) string {
		return signCheck(f, a, "positive", c.uses)
	}},
	{"negative", func(f *ast.Field, a string, _ *ast.Decorator, c emitCtx) string {
		return signCheck(f, a, "negative", c.uses)
	}},
	{"multipleOf", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string { return multipleOfCheck(f, a, d, c.uses) }},

	// array
	{"minItems", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return itemsBoundCheck(f, a, d, ">=", "minItems", c.uses)
	}},
	{"maxItems", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string {
		return itemsBoundCheck(f, a, d, "<=", "maxItems", c.uses)
	}},
	{"uniqueItems", func(f *ast.Field, a string, _ *ast.Decorator, c emitCtx) string { return uniqueItemsCheck(f, a, c.uses) }},

	// file
	{"maxSize", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string { return maxSizeCheck(f, a, d, c.uses) }},
	{"mimeTypes", func(f *ast.Field, a string, d *ast.Decorator, c emitCtx) string { return mimeTypesCheck(f, a, d, c.uses) }},
}

// validatorByName returns the registry entry for `name`, or nil when the
// decorator isn't a recognised validator (metadata decorators like
// `@doc` / `@deprecated` / `@example` fall through here and produce no
// runtime check).
func validatorByName(name string) *validatorEntry {
	for i := range validators {
		if validators[i].name == name {
			return &validators[i]
		}
	}
	return nil
}

// fieldChecksWithPkg dispatches each decorator on a field through the
// validator registry. The lookup is by name; the matched validator's
// `emit` closure is responsible for type-guarding (skip silently when
// the field type doesn't fit) and producing the Go source for the check.
//
// Adding a new decorator-driven validator is a single new entry in
// `validators` (see [validatorEntry]) — no edits to this dispatcher.
func fieldChecksWithPkg(f *ast.Field, pkg *semantic.Package, uses map[string]bool) []string {
	access := "v." + GoFieldName(f.Name)
	ctx := emitCtx{pkg: pkg, uses: uses}
	var out []string
	for _, d := range f.Decorators {
		v := validatorByName(d.Name)
		if v == nil {
			continue
		}
		if s := v.emit(f, access, d, ctx); s != "" {
			out = append(out, s)
		}
	}
	return out
}
