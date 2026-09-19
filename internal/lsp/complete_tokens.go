// Token-level helpers: duration/size unit completions + keyword list + extend-service context.
package lsp

import (
	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// identBefore returns the identifier token immediately before t (skipping
// only whitespace, which the tokenizer has already stripped). Returns ok
// = false when the previous token is not an identifier.
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
	return unitCompletions(prev, mid, "duration", lexer.DurationUnits, durationPresets)
}

// sizeCompletions is the byte-size analogue of [durationCompletions].
func sizeCompletions(prev, mid *lexer.Token) []protocol.CompletionItem {
	return unitCompletions(prev, mid, "size", lexer.SizeSuffixes(), sizePresets)
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

// isExtendServiceContext reports whether the cursor sits at the
// identifier slot of an `extend service <cursor>` clause: the two
// tokens before it, skipping any partial ident being typed, are
// `service` then `extend`.
//
// `end` is the exclusive bound of the tokens that precede the cursor.
// [scanFromIndex] computes it for both cursor shapes and, crucially,
// steps over the EOF token the stream always ends with - reading the
// slice's last entry instead would see EOF as the previous token and
// never fire on a clause the user is typing at the end of the buffer.
func isExtendServiceContext(view snapshotView, pos protocol.Position) bool {
	idx, _ := view.tokenAt(pos.Line, pos.Character)
	target := lexer.Position{Line: int(pos.Line) + 1, Column: int(pos.Character) + 1}
	end := scanFromIndex(view, idx, target) + 1
	// The service name the user is mid-typing is not part of the prefix
	// being matched.
	if idx >= 0 && idx < len(view.tokens) && view.tokens[idx].Kind == lexer.Ident {
		end = idx
	}
	if end < 2 {
		return false
	}
	return view.tokens[end-1].Kind == lexer.KwService && view.tokens[end-2].Kind == lexer.KwExtend
}

// keywordCompletions surfaces the named reserved keywords as completion
// items, in catalogue order. Callers pass the set that is legal where
// the cursor sits - a keyword offered outside the block that accepts it
// is a suggestion the parser would reject.
//
// The high-traffic declaration keywords (`type`, `service`, `error`,
// `enum`, `scalar`, `middleware`, `extend`, the verb set) carry snippet
// expansions so Tab-completes a fully-shaped skeleton with the cursor
// landing at the body's first edit point - the keyword set is exactly
// what the user types most when scaffolding a new file, and the snippet
// payoff outweighs the popup verbosity.
func keywordCompletions(want ...string) []protocol.CompletionItem {
	type entry struct {
		label   string
		snippet string // empty means plain insert (no snippet)
	}
	entries := []entry{
		{"package", "package $1"},
		{"import", "import \"$1\""},
		{"type", "type ${1:Name} {\n\t$0\n}"},
		{"enum", "enum ${1:Name} {\n\t$0\n}"},
		{"error", "error ${1|BadRequest,Unauthorized,Forbidden,NotFound,Conflict,UnprocessableEntity,TooManyRequests,Internal|} ${2:Name}"},
		{"scalar", "scalar ${1:Name} ${2|string,int,int32,int64,uint,float64,bool,bytes|}"},
		{"service", "service ${1:Name} {\n\t$0\n}"},
		{"extend", "extend service ${1:Name} {\n\t$0\n}"},
		{"middleware", "middleware ${1:Name}"},
		{"event", "event ${1:Name} {\n\tpayload ${2:Payload}\n}"},
		{"request", "request ${1:Type}"},
		{"response", "response ${1:Type}"},
		{"payload", "payload ${1:Type}"},
		{"map", "map<${1:string}, ${2:string}>"},
		{"get", "get ${1:Name} /${2:path} {\n\trequest  ${3:Req}\n\tresponse ${4:Resp}\n}"},
		{"post", "post ${1:Name} /${2:path} {\n\trequest  ${3:Req}\n\tresponse ${4:Resp}\n}"},
		{"put", "put ${1:Name} /${2:path} {\n\trequest  ${3:Req}\n\tresponse ${4:Resp}\n}"},
		{"patch", "patch ${1:Name} /${2:path} {\n\trequest  ${3:Req}\n\tresponse ${4:Resp}\n}"},
		{"delete", "delete ${1:Name} /${2:path} {\n\trequest  ${3:Req}\n\tresponse ${4:Resp}\n}"},
		{"head", "head ${1:Name} /${2:path} {\n\tresponse ${3:Resp}\n}"},
		{"options", "options ${1:Name} /${2:path} {\n\tresponse ${3:Resp}\n}"},
		// True / false / null are literal keywords - no snippet, just the value.
		{"true", ""},
		{"false", ""},
		{"null", ""},
	}
	keep := make(map[string]bool, len(want))
	for _, w := range want {
		keep[w] = true
	}
	out := make([]protocol.CompletionItem, 0, len(want))
	for _, e := range entries {
		if !keep[e.label] {
			continue
		}
		item := protocol.CompletionItem{
			Label: e.label,
			Kind:  protocol.CompletionItemKindKeyword,
		}
		if e.snippet != "" {
			item.InsertText = e.snippet
			item.InsertTextFormat = protocol.InsertTextFormatSnippet
		}
		out = append(out, item)
	}
	return out
}
