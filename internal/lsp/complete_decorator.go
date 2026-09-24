package lsp

import (
	"fmt"
	"sort"
	"strings"

	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/errcat"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// decoratorArgItems offers the argument candidates of `@name(...)`, or nil
// when the slot has no closed set.
func (s *Server) decoratorArgItems(view snapshotView, pos protocol.Position, currentURI, currentSrc, name string, prev, mid *lexer.Token) []protocol.CompletionItem {
	if name == "middlewares" {
		return s.middlewareNameCompletions(currentURI, currentSrc)
	}
	if name == "errors" {
		return s.errorNameCompletions(currentURI, currentSrc)
	}
	if name == "status" {
		return httpStatusCompletions()
	}
	if name == "security" {
		if items := s.securitySchemeCompletions(currentURI); items != nil {
			return items
		}
	}
	if name == "default" {
		if items := s.defaultValueCompletions(view, pos, currentURI, currentSrc); items != nil {
			return items
		}
	}
	if spec, ok := semantic.Registry[name]; ok && len(spec.Args.Kinds) > 0 {
		switch spec.Args.Kinds[0] {
		case semantic.ArgDuration:
			return durationCompletions(prev, mid)
		case semantic.ArgSize:
			return sizeCompletions(prev, mid)
		}
	}
	return decoratorArgCompletions(name)
}

// httpStatusCompletions offers common HTTP status codes for `@status(...)`,
// with the reason phrase as detail.
func httpStatusCompletions() []protocol.CompletionItem {
	type entry struct {
		code   string
		phrase string
	}
	entries := []entry{
		{"200", "OK"},
		{"201", "Created"},
		{"202", "Accepted"},
		{"204", "No Content"},
		{"301", "Moved Permanently"},
		{"302", "Found"},
		{"304", "Not Modified"},
		{"307", "Temporary Redirect"},
		{"308", "Permanent Redirect"},
		{"400", "Bad Request"},
		{"401", "Unauthorized"},
		{"403", "Forbidden"},
		{"404", "Not Found"},
		{"409", "Conflict"},
		{"422", "Unprocessable Entity"},
		{"429", "Too Many Requests"},
		{"500", "Internal Server Error"},
		{"502", "Bad Gateway"},
		{"503", "Service Unavailable"},
		{"504", "Gateway Timeout"},
	}
	out := make([]protocol.CompletionItem, 0, len(entries))
	for _, e := range entries {
		out = append(out, protocol.CompletionItem{
			Label:      e.code,
			Kind:       protocol.CompletionItemKindValue,
			Detail:     "HTTP " + e.code + " " + e.phrase,
			InsertText: e.code,
		})
	}
	return out
}

// decoratorArgContext reports whether pos is inside a `@name(...)` argument
// list and returns name. The backward walk starts at the cursor's own token
// (a cursor on the `(` counts), or on whitespace at the last token before it.
func decoratorArgContext(view snapshotView, pos protocol.Position) (string, bool) {
	idx, _ := view.tokenAt(pos.Line, pos.Character)
	start := idx
	if idx < 0 {
		target := lexer.Position{Line: int(pos.Line) + 1, Column: int(pos.Character) + 1}
		start = scanFromIndex(view, idx, target)
	}
	depth := 0
	for i := start; i >= 0; i-- {
		if i >= len(view.tokens) {
			continue
		}
		t := view.tokens[i]
		switch t.Kind {
		case lexer.RParen:
			// The cursor's own `)` closes the list it is in.
			if i == idx {
				continue
			}
			depth++
		case lexer.LParen:
			if depth > 0 {
				depth--
				continue
			}
			// The unmatched `(`: a decorator when `@` is two tokens back (the
			// name may be spelt like a keyword).
			if i >= 2 && view.tokens[i-2].Kind == lexer.At {
				return view.tokens[i-1].Text, true
			}
			return "", false
		}
	}
	return "", false
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
// level of pos whose name starts with prefix.
func decoratorCompletions(view snapshotView, pos protocol.Position, prefix string) []protocol.CompletionItem {
	level := guessLevel(view, pos)
	// On a field or scalar, the type's primitive category must meet the
	// decorator's AppliesTo; 0 on either side means no filter.
	var fieldPrim semantic.Prims
	switch level {
	case semantic.LvlField:
		fieldPrim = fieldPrimAt(view, pos)
	case semantic.LvlScalar:
		fieldPrim = scalarPrimAt(view, pos)
	}
	// An `extend service` takes the service decorators that have a method form,
	// plus @group.
	extendSite := level == semantic.LvlService && nextDeclDecoratorIsExtend(view, pos)
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
