// Token-level helpers: duration/size unit completions + keyword list + extend-service context.
package lsp

import (
	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/lexer"
)

func identBefore(view snapshotView, t *lexer.Token) (string, bool) {
	idx := -1
	for i := range view.tokens {
		if &view.tokens[i] == t {
			idx = i
			break
		}
	}
	if idx <= 0 {
		return "", false
	}
	prev := view.tokens[idx-1]
	if prev.Kind != lexer.Ident {
		return "", false
	}
	return prev.Text, true
}

// importPathCompletions walks the design root and returns one item per
// subdirectory that contains at least one `.craftgo` file. Labels are
// the directory path relative to the design root, matching the literal
// the user is expected to type inside `import "…"` (e.g. `shared`,
// `v1/api`, `auth/oauth`). The current file's own directory is
// filtered out so users do not import themselves.

var durationSuffixes = []string{"ns", "us", "µs", "ms", "s", "m", "h"}
var sizeSuffixes = []string{"B", "KB", "MB", "GB"}

// durationPresets / sizePresets are the values surfaced when the
// cursor is inside an empty argument slot (just after `(`) so users
// who don't have a number in mind get a sensible starter list.
var durationPresets = []string{"100ms", "500ms", "1s", "5s", "10s", "30s", "1m", "5m"}
var sizePresets = []string{"1KB", "10KB", "100KB", "1MB", "10MB", "100MB"}

// durationCompletions surfaces duration-typed completions for the
// cursor's slot. When the user has typed a bare digit run (Int token
// at or just-before cursor), each suffix is paired with that prefix
// and emitted as a TextEdit replacing the Int. Otherwise a curated
// preset list is offered.
func durationCompletions(prev, mid *lexer.Token) []protocol.CompletionItem {
	return unitCompletions(prev, mid, "duration", durationSuffixes, durationPresets)
}

// sizeCompletions is the byte-size analogue of [durationCompletions].
func sizeCompletions(prev, mid *lexer.Token) []protocol.CompletionItem {
	return unitCompletions(prev, mid, "size", sizeSuffixes, sizePresets)
}

// unitCompletions builds the suffix / preset list for both duration
// and size paths. When mid OR prev is a bare Int token (cursor
// inside the digits, or right at their trailing edge) the digits
// become the prefix and TextEdit-bound completions replace the
// existing Int. Otherwise the preset list flows through.
func unitCompletions(prev, mid *lexer.Token, detail string, suffixes, presets []string) []protocol.CompletionItem {
	intTok := pickIntForUnit(prev, mid)
	if intTok != nil {
		editRange := rangeOf(*intTok)
		out := make([]protocol.CompletionItem, 0, len(suffixes))
		for _, u := range suffixes {
			value := intTok.Text + u
			edit := protocol.TextEdit{Range: editRange, NewText: value}
			out = append(out, protocol.CompletionItem{
				Label:    value,
				Kind:     protocol.CompletionItemKindValue,
				Detail:   detail,
				TextEdit: &edit,
			})
		}
		return out
	}
	out := make([]protocol.CompletionItem, 0, len(presets))
	for _, p := range presets {
		out = append(out, protocol.CompletionItem{
			Label:      p,
			Kind:       protocol.CompletionItemKindValue,
			Detail:     detail,
			InsertText: p,
		})
	}
	return out
}

// pickIntForUnit returns the Int token the cursor is editing when one
// of mid / prev is a bare digit literal. tokenAt's inclusive
// end-column rule can resolve the cursor between an Int and a
// trailing punctuator (`@timeout(10|)`) onto the punctuator, so
// `prev` is the secondary anchor.
func pickIntForUnit(prev, mid *lexer.Token) *lexer.Token {
	if mid != nil && mid.Kind == lexer.Int {
		return mid
	}
	if prev != nil && prev.Kind == lexer.Int {
		return prev
	}
	return nil
}

// decoratorArgCompletions returns enum-value completions for the
// decorator at the cursor when the spec restricts them. Returns nil
// to signal "no enum applies - let the next branch handle this
// position".

func isExtendServiceContext(view snapshotView, pos protocol.Position) bool {
	idx, _ := view.tokenAt(pos.Line, pos.Character)
	if idx < 0 {
		idx = len(view.tokens)
	}
	// Skip a partial ident at the cursor - the user is mid-typing
	// the service name and we still want to fire.
	if idx >= 0 && idx < len(view.tokens) && view.tokens[idx].Kind == lexer.Ident {
		idx--
	}
	if idx < 2 {
		return false
	}
	prev := view.tokens[idx-1]
	prev2 := view.tokens[idx-2]
	return prev.Kind == lexer.KwService && prev2.Kind == lexer.KwExtend
}

// serviceNameCompletions enumerates primary `service Name`
// declarations that are valid extension targets from the cursor's
// current file. Extends resolve per-package, so cross-package
// services would always trip `service/extend-orphan` - including
// them in the completion list would mislead the user. The function
// therefore filters by the current file's package name.

func keywordCompletions() []protocol.CompletionItem {
	kw := []string{
		"package", "import", "type", "enum", "error", "scalar",
		"service", "extend", "middleware", "request", "response",
		"map", "true", "false", "null",
		"get", "post", "put", "patch", "delete", "head", "options",
	}
	out := make([]protocol.CompletionItem, 0, len(kw))
	for _, k := range kw {
		out = append(out, protocol.CompletionItem{
			Label: k,
			Kind:  protocol.CompletionItemKindKeyword,
		})
	}
	return out
}
