package lsp

import (
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"

	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/errcat"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// decoratorArgItems offers the argument candidates of `@name(...)`, or nil
// when the slot has no closed set.
func (r *request) decoratorArgItems(c cursor, name string) []protocol.CompletionItem {
	if name == "middlewares" {
		return r.middlewareNameCompletions()
	}
	if name == "errors" {
		return r.errorNameCompletions()
	}
	if name == "status" {
		return httpStatusCompletions()
	}
	if name == "security" {
		if items := r.securitySchemeCompletions(); items != nil {
			return items
		}
	}
	if name == "default" {
		if items := r.defaultValueCompletions(c); items != nil {
			return items
		}
	}
	if spec, ok := semantic.Registry[name]; ok && len(spec.Args.Kinds) > 0 {
		switch spec.Args.Kinds[0] {
		case semantic.ArgDuration:
			return durationCompletions(r.view(), c)
		case semantic.ArgSize:
			return sizeCompletions(r.view(), c)
		}
	}
	return decoratorArgCompletions(name)
}

// successStatuses are the success and redirect codes `@status(...)` offers
// beside the statuses of the error categories.
var successStatuses = []int{200, 201, 202, 204, 301, 302, 304, 307, 308}

// httpStatusCompletions offers [successStatuses] and each error category's
// status for `@status(...)`, with the reason phrase as detail.
func httpStatusCompletions() []protocol.CompletionItem {
	codes := slices.Clone(successStatuses)
	for _, c := range errcat.Categories {
		codes = append(codes, c.Status)
	}
	slices.Sort(codes)
	codes = slices.Compact(codes)
	out := make([]protocol.CompletionItem, 0, len(codes))
	for _, code := range codes {
		label := strconv.Itoa(code)
		out = append(out, protocol.CompletionItem{
			Label:      label,
			Kind:       protocol.CompletionItemKindValue,
			Detail:     "HTTP " + label + " " + http.StatusText(code),
			InsertText: label,
		})
	}
	return out
}

// decoratorArgContext reports whether the cursor is inside a `@name(...)`
// argument list, one whose `(` starts before the cursor and whose `)` does
// not, and returns name and the index of the `(`.
func decoratorArgContext(view snapshotView, c cursor) (string, int, bool) {
	depth := 0
	for i := view.lastBefore(c); i >= 0; i-- {
		switch view.tokens[i].Kind {
		case lexer.RParen:
			depth++
		case lexer.LParen:
			if depth > 0 {
				depth--
				continue
			}
			// The unmatched `(`: a decorator when `@` is two tokens back (the
			// name may be spelt like a keyword).
			if i >= 2 && view.tokens[i-2].Kind == lexer.At {
				return view.tokens[i-1].Text, i, true
			}
			return "", 0, false
		}
	}
	return "", 0, false
}

// decoratorArgCompletions offers the registry's argument values of @name (the
// formats of @format, for one), or nil when it declares none.
func decoratorArgCompletions(name string) []protocol.CompletionItem {
	spec, ok := semantic.Registry[name]
	if !ok {
		return nil
	}
	values := spec.Args.Enum
	if len(values) == 0 {
		return nil
	}
	out := make([]protocol.CompletionItem, 0, len(values))
	for _, v := range values {
		out = append(out, protocol.CompletionItem{
			Label:      v,
			Kind:       protocol.CompletionItemKindEnumMember,
			Detail:     "@" + name + " value",
			InsertText: v,
		})
	}
	return out
}

// decoratorCompletions offers the registered decorators legal at the site
// level of the cursor whose name starts with prefix.
func decoratorCompletions(view snapshotView, c cursor, prefix string) []protocol.CompletionItem {
	level := guessLevel(view, c)
	// On a field or scalar, the type's primitive category must meet the
	// decorator's AppliesTo; 0 on either side means no filter.
	var fieldPrim semantic.Prims
	switch level {
	case semantic.LvlField:
		fieldPrim = fieldPrimAt(view, c)
	case semantic.LvlScalar:
		fieldPrim = scalarPrimAt(view, c)
	}
	// An `extend service` takes the service decorators that have a method form,
	// plus @group.
	extendSite := level == semantic.LvlService && nextTopLevelKeyword(view, c) == lexer.KwExtend
	names := make([]string, 0, len(semantic.Registry))
	for name := range semantic.Registry {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]protocol.CompletionItem, 0, len(names))
	for _, name := range names {
		spec := semantic.Registry[name]
		if spec.Levels == 0 || spec.Levels&level == 0 {
			continue
		}
		if extendSite && spec.Levels&semantic.LvlMethod == 0 && name != "group" {
			continue
		}
		if fieldPrim != 0 && spec.AppliesTo != 0 && spec.AppliesTo&fieldPrim == 0 {
			continue
		}
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			continue
		}
		insert := name
		if needsArgs(spec.Args) {
			insert = name + "($0)"
		}
		out = append(out, protocol.CompletionItem{
			Label:            name,
			Kind:             protocol.CompletionItemKindFunction,
			Detail:           argsRuleSummary(spec.Args),
			Documentation:    spec.Doc,
			InsertText:       insert,
			InsertTextFormat: protocol.InsertTextFormatSnippet,
		})
	}
	return out
}

// needsArgs reports whether a decorator requires arguments; its completion then
// inserts `name($0)`.
func needsArgs(r semantic.ArgsRule) bool {
	return r.Min > 0 || r.Variadic != 0
}

// errorCategoryCompletions offers each of [errcat.Categories] for `error |`,
// with its HTTP status as detail.
func errorCategoryCompletions() []protocol.CompletionItem {
	out := make([]protocol.CompletionItem, 0, len(errcat.Categories))
	for _, c := range errcat.Categories {
		detail := fmt.Sprintf("HTTP %d", c.Status)
		doc := protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: fmt.Sprintf("**`%s`** - built-in error category (HTTP %d).\n\nUse as `error %s YourErrorName` to declare an error of this kind.", c.Name, c.Status, c.Name),
		}
		out = append(out, protocol.CompletionItem{
			Label:         c.Name,
			Kind:          protocol.CompletionItemKindEnumMember,
			Detail:        detail,
			Documentation: doc,
		})
	}
	return out
}
