package lsp

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/designopts"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// defaultValueCompletions answers `@default(|)` from the field's type: an
// enum's values, or true and false for a bool or a scalar over one; else nil.
func (r *request) defaultValueCompletions(c cursor) []protocol.CompletionItem {
	f := fieldAtCursor(r.view(), c)
	if f == nil || f.Type == nil || f.Type.Map != nil || f.Type.Array || f.Type.Named == nil || f.Type.Named.Name == nil ||
		typedByTypeParam(r.view(), f) {
		return nil
	}
	name := f.Type.Named.Name.String()
	if sp, ok := prims.Lookup(name); ok {
		if sp.Kind == prims.Bool {
			return boolLiteralCompletions()
		}
		return nil
	}
	switch d := r.project().lookup(name, semantic.EnumDecls|semantic.ScalarDecls).(type) {
	case *ast.EnumDecl:
		enumVals := d.EnumValues()
		out := make([]protocol.CompletionItem, 0, len(enumVals))
		for _, val := range enumVals {
			out = append(out, protocol.CompletionItem{
				Label:      val.Name,
				Kind:       protocol.CompletionItemKindEnumMember,
				Detail:     "value of enum " + d.Name,
				InsertText: val.Name,
			})
		}
		return out
	case *ast.ScalarDecl:
		if sp, ok := prims.Lookup(d.Primitive); ok && sp.Kind == prims.Bool {
			return boolLiteralCompletions()
		}
	}
	return nil
}

// boolLiteralCompletions offers true and false.
func boolLiteralCompletions() []protocol.CompletionItem {
	return []protocol.CompletionItem{
		{Label: "true", Kind: protocol.CompletionItemKindValue, Detail: "bool", InsertText: "true"},
		{Label: "false", Kind: protocol.CompletionItemKindValue, Detail: "bool", InsertText: "false"},
	}
}

// pathParamCompletions answers `/{|}` with the route variables the request's
// fields, mixin fields included, bind and the template does not name yet; nil
// without a request clause. The clause is read from tokens: the parser takes
// an unfinished `{}` for the body.
func (r *request) pathParamCompletions(brace int) []protocol.CompletionItem {
	view := r.view()
	name := requestTypeAfter(view, brace)
	if name == "" {
		return nil
	}
	v := r.project()
	td, ok := v.lookup(name, semantic.TypeDecls).(*ast.TypeDecl)
	if !ok {
		return nil
	}
	qualifier, _, qualified := strings.Cut(name, ".")
	if !qualified {
		qualifier = ""
	}
	current := v.currentPackage()
	used := pathParamsBefore(view, brace)
	var out []protocol.CompletionItem
	for _, ff := range semantic.FlattenFields(td, qualifier, semantic.NewResolver(v.proj, current), nil) {
		param, ok := v.proj.PathParam(current, ff.Field)
		if !ok || used[param] {
			continue
		}
		out = append(out, protocol.CompletionItem{
			Label:         param,
			Kind:          protocol.CompletionItemKindField,
			Detail:        ff.Field.Type.String() + " - field of " + td.Name,
			Documentation: strings.Join(ff.Field.Doc, "\n"),
			InsertText:    param,
		})
	}
	return out
}

// requestTypeAfter returns the type of the `request` clause after token i, or
// "" when the method has none; the scan stops at the next verb or declaration.
func requestTypeAfter(view snapshotView, i int) string {
	for j := i + 1; j < len(view.tokens); j++ {
		t := view.tokens[j]
		switch {
		case t.Kind == lexer.KwRequest:
			if j+1 >= len(view.tokens) || view.tokens[j+1].Kind != lexer.Ident {
				return ""
			}
			return qualifiedNameAt(view, j+1)
		case t.Kind.IsVerb() || isDeclKeyword(t.Kind):
			return ""
		}
	}
	return ""
}

// pathParamsBefore returns the parameters the template binds before brace i,
// on its line.
func pathParamsBefore(view snapshotView, i int) map[string]bool {
	used := map[string]bool{}
	line := view.tokens[i].Pos.Line
	for j := 0; j+2 < i; j++ {
		if view.tokens[j].Pos.Line != line || view.tokens[j].Kind != lexer.LBrace {
			continue
		}
		if view.tokens[j+2].Kind == lexer.RBrace {
			used[view.tokens[j+1].Text] = true
		}
	}
	return used
}

// serviceNameCompletions offers the primary services of the buffer's package,
// the names an `extend service` can target.
func (r *request) serviceNameCompletions() []protocol.CompletionItem {
	v := r.project()
	current := v.currentPackage()
	pkg := v.proj.Packages[current]
	if pkg == nil {
		return nil
	}
	var out []protocol.CompletionItem
	for _, d := range pkg.Decls(semantic.ServiceDecls) {
		out = append(out, declItem(d, current, current))
	}
	return out
}

// declItem is the completion item of d, a declaration of package pkg offered
// in package current; another package's declaration names its package in the
// detail.
func declItem(d ast.Decl, pkg, current string) protocol.CompletionItem {
	info := infoOf(d)
	detail := info.summary
	if pkg != current {
		detail += " (" + pkg + ")"
	}
	return protocol.CompletionItem{
		Label:         d.DeclName(),
		Kind:          info.item,
		Detail:        detail,
		Documentation: strings.Join(info.doc, "\n"),
		InsertText:    d.DeclName(),
	}
}

// projectDeclItems offers the declarations of kinds across the project, one per
// name (the first package by name wins), sorted by label.
func (r *request) projectDeclItems(kinds semantic.DeclKind) []protocol.CompletionItem {
	v := r.project()
	current := v.currentPackage()
	seen := map[string]bool{}
	var out []protocol.CompletionItem
	for _, pkgName := range slices.Sorted(maps.Keys(v.proj.Packages)) {
		for _, d := range v.proj.Packages[pkgName].Decls(kinds) {
			if !seen[d.DeclName()] {
				seen[d.DeclName()] = true
				out = append(out, declItem(d, pkgName, current))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// securitySchemeCompletions offers the manifest's openapi.securitySchemes, with
// each scheme's type and its scheme or location as detail; nil when there are none.
func (r *request) securitySchemeCompletions() []protocol.CompletionItem {
	cfg, _ := designopts.ProjectOf(r.path)
	if cfg == nil || len(cfg.OpenAPI.SecuritySchemes) == 0 {
		return nil
	}
	out := make([]protocol.CompletionItem, 0, len(cfg.OpenAPI.SecuritySchemes))
	for name, scheme := range cfg.OpenAPI.SecuritySchemes {
		detail := scheme.Type
		switch {
		case scheme.Scheme != "":
			detail = scheme.Type + " " + scheme.Scheme
		case scheme.In != "" && scheme.Name != "":
			detail = scheme.Type + " (" + scheme.In + " " + scheme.Name + ")"
		}
		doc := protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: fmt.Sprintf("**`%s`** - %s security scheme.\n\nDeclared in `craftgo.design.yaml` under `openapi.securitySchemes.%s`.", name, scheme.Type, name),
		}
		out = append(out, protocol.CompletionItem{
			Label:         name,
			Kind:          protocol.CompletionItemKindEnumMember,
			Detail:        detail,
			Documentation: doc,
			InsertText:    name,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// middlewareNameCompletions offers every middleware in the project.
func (r *request) middlewareNameCompletions() []protocol.CompletionItem {
	return r.projectDeclItems(semantic.MiddlewareDecls)
}

// errorNameCompletions offers every error in the project.
func (r *request) errorNameCompletions() []protocol.CompletionItem {
	return r.projectDeclItems(semantic.ErrorDecls)
}

// typeCompletionsProjectWide offers what a field type can name: the built-ins,
// `map`, and every type, enum and scalar in the project.
func (r *request) typeCompletionsProjectWide() []protocol.CompletionItem {
	items := primitiveCompletions()
	items = append(items, keywordCompletions("map")...)
	items = append(items, r.declCompletions(semantic.TypeRefDecls)...)
	return items
}

// primitiveCompletions offers every built-in.
func primitiveCompletions() []protocol.CompletionItem {
	var items []protocol.CompletionItem
	for _, sp := range prims.All() {
		items = append(items, protocol.CompletionItem{
			Label:  sp.Name,
			Kind:   protocol.CompletionItemKindKeyword,
			Detail: "built-in",
		})
	}
	return items
}

// scalarPrimitiveCompletions offers [semantic.ScalarPrimitives] for
// `scalar Name |`.
func scalarPrimitiveCompletions() []protocol.CompletionItem {
	var out []protocol.CompletionItem
	for _, name := range semantic.ScalarPrimitives() {
		out = append(out, protocol.CompletionItem{
			Label:  name,
			Kind:   protocol.CompletionItemKindKeyword,
			Detail: "built-in",
		})
	}
	return out
}

// clauseTypeCompletions offers the `type` declarations, the only kind a
// `request`, `response` or `payload` clause accepts.
func (r *request) clauseTypeCompletions() []protocol.CompletionItem {
	return r.declCompletions(semantic.TypeDecls)
}

// declCompletions offers every declaration of kinds in the project - bare in
// the buffer's package, `pkg.Name` elsewhere - plus each other package as `pkg.`.
func (r *request) declCompletions(kinds semantic.DeclKind) []protocol.CompletionItem {
	v := r.project()
	currentPkg := v.currentPackage()
	var items []protocol.CompletionItem
	for _, pkgName := range slices.Sorted(maps.Keys(v.proj.Packages)) {
		for _, d := range v.proj.Packages[pkgName].Decls(kinds) {
			item := declItem(d, pkgName, currentPkg)
			if pkgName != currentPkg {
				item.Label = pkgName + "." + item.Label
				item.InsertText = item.Label
			}
			items = append(items, item)
		}
	}
	for _, pkgName := range slices.Sorted(maps.Keys(v.proj.Packages)) {
		if pkgName == currentPkg {
			continue
		}
		items = append(items, protocol.CompletionItem{
			Label:      pkgName,
			Kind:       protocol.CompletionItemKindModule,
			Detail:     "package",
			InsertText: pkgName + ".",
		})
	}
	return items
}
