package lsp

import (
	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// identBefore returns the text of the token before t when it is an identifier.
func identBefore(view snapshotView, t *lexer.Token) (string, bool) {
	idx := tokenIndex(view, t)
	if idx <= 0 {
		return "", false
	}
	prev := view.tokens[idx-1]
	if prev.Kind != lexer.Ident {
		return "", false
	}
	return prev.Text, true
}

// durationPresets and sizePresets fill an argument with no digits typed.
var durationPresets = []string{"100ms", "500ms", "1s", "5s", "10s", "30s", "1m", "5m"}
var sizePresets = []string{"1KB", "10KB", "100KB", "1MB", "10MB", "100MB"}

// durationCompletions is [unitCompletions] for a duration argument.
func durationCompletions(prev, mid *lexer.Token) []protocol.CompletionItem {
	return unitCompletions(prev, mid, "duration", lexer.DurationUnits, durationPresets)
}

// sizeCompletions is the byte-size analogue of [durationCompletions].
func sizeCompletions(prev, mid *lexer.Token) []protocol.CompletionItem {
	return unitCompletions(prev, mid, "size", lexer.SizeSuffixes(), sizePresets)
}

// unitCompletions offers the typed digits joined with each suffix, as edits
// replacing the digits, or the presets when no digits are typed.
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

// pickIntForUnit returns the Int token under or before the cursor; tokenAt
// resolves `@timeout(10|)` to the `)`, leaving the digits in prev.
func pickIntForUnit(prev, mid *lexer.Token) *lexer.Token {
	if mid != nil && mid.Kind == lexer.Int {
		return mid
	}
	if prev != nil && prev.Kind == lexer.Int {
		return prev
	}
	return nil
}

// isExtendServiceContext reports whether the cursor is in the name slot of
// `extend service |`, a partly typed name included.
func isExtendServiceContext(view snapshotView, pos protocol.Position) bool {
	idx, _ := view.tokenAt(pos.Line, pos.Character)
	target := lexer.Position{Line: int(pos.Line) + 1, Column: int(pos.Character) + 1}
	end := scanFromIndex(view, idx, target) + 1
	if idx >= 0 && idx < len(view.tokens) && view.tokens[idx].Kind == lexer.Ident {
		end = idx
	}
	if end < 2 {
		return false
	}
	return view.tokens[end-1].Kind == lexer.KwService && view.tokens[end-2].Kind == lexer.KwExtend
}

// keywordCompletions offers the keywords in want, in catalogue order, as
// snippets where the keyword has one.
func keywordCompletions(want ...string) []protocol.CompletionItem {
	type entry struct {
		label   string
		snippet string // empty: plain insert
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
