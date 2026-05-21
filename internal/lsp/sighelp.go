package lsp

import (
	"context"
	"encoding/json"
	"strings"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// onSignatureHelp answers `textDocument/signatureHelp`. Surfaces the
// decorator-registry parameter list so the editor's parameter-hint
// popup follows the cursor through a `@name(arg1, arg2, ...)` call.
// The DSL is structurally simple - only decorator args carry typed
// parameters - so this handler does NOT attempt to drive hints from
// the in-file AST. Activates only when the cursor lives inside a
// decorator argument list.
func (s *Server) onSignatureHelp(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	var params protocol.SignatureHelpParams
	if err := json.Unmarshal(req.Params(), &params); err != nil {
		return reply(ctx, nil, err)
	}
	src := s.snapshot(params.TextDocument.URI)
	if src == "" {
		return reply(ctx, nil, nil)
	}
	view := parseSnapshot(string(params.TextDocument.URI), src)
	name, ok := decoratorArgContext(view, params.Position)
	if !ok {
		return reply(ctx, nil, nil)
	}
	spec, ok := semantic.Registry[name]
	if !ok {
		return reply(ctx, nil, nil)
	}
	label, paramLabels := decoratorSignatureLabel(name, spec)
	if label == "" {
		return reply(ctx, nil, nil)
	}
	paramInfos := make([]protocol.ParameterInformation, 0, len(paramLabels))
	for _, p := range paramLabels {
		paramInfos = append(paramInfos, protocol.ParameterInformation{Label: p})
	}
	active := activeParamIndex(view, params.Position, len(paramLabels))
	sig := protocol.SignatureInformation{
		Label:           label,
		Documentation:   spec.Doc,
		Parameters:      paramInfos,
		ActiveParameter: uint32(active),
	}
	return reply(ctx, &protocol.SignatureHelp{
		Signatures:      []protocol.SignatureInformation{sig},
		ActiveSignature: 0,
		ActiveParameter: uint32(active),
	}, nil)
}

// decoratorSignatureLabel renders a `@name(p1, p2, ...)` style label
// from the registry's argument shape. Returns the label PLUS the
// individual parameter labels so [protocol.ParameterInformation]
// entries can point at each slot for highlighting. For variadic
// decorators (`@middlewares(A, B, ...)`) the label includes a
// trailing `...` placeholder so the user sees there is no fixed
// arity ceiling.
func decoratorSignatureLabel(name string, spec semantic.Spec) (string, []string) {
	rule := spec.Args
	var parts []string
	if rule.Variadic != 0 {
		parts = append(parts, argKindName(rule.Variadic)+"...")
	} else if len(rule.Kinds) > 0 {
		for _, k := range rule.Kinds {
			parts = append(parts, argKindName(k))
		}
	} else if rule.Min == 0 && rule.Max == 0 {
		return "@" + name, nil
	}
	if len(parts) == 0 {
		return "@" + name, nil
	}
	return "@" + name + "(" + strings.Join(parts, ", ") + ")", parts
}

// argKindName turns an [semantic.ArgKind] into a short human label
// suitable for a signature-help parameter slot. Mirrors the table
// the user already sees in the README and on hover so the popup
// stays in sync without duplicating descriptions.
func argKindName(k semantic.ArgKind) string {
	switch k {
	case semantic.ArgString:
		return "string"
	case semantic.ArgInt:
		return "int"
	case semantic.ArgNumber:
		return "number"
	case semantic.ArgBool:
		return "bool"
	case semantic.ArgIdent:
		return "ident"
	case semantic.ArgStringOrIdent:
		return "string|ident"
	case semantic.ArgDuration:
		return "duration"
	case semantic.ArgSize:
		return "size"
	case semantic.ArgAny:
		return "any"
	}
	return "?"
}

// activeParamIndex walks tokens from the cursor backward to the
// opening `(` of the enclosing decorator and counts the commas in
// between - the count is the zero-based active parameter slot. Caps
// the result at `max-1` so variadic decorators do not push the
// highlight past the last documented slot.
func activeParamIndex(view snapshotView, pos protocol.Position, max int) int {
	idx, _ := view.tokenAt(pos.Line, pos.Character)
	if idx < 0 {
		idx = len(view.tokens)
	}
	commas := 0
	depth := 0
	for i := idx - 1; i >= 0; i-- {
		t := view.tokens[i]
		switch t.Kind {
		case 0:
			// reserved zero kind - skip defensively
		}
		switch {
		case t.Text == ")":
			depth++
		case t.Text == "(":
			if depth > 0 {
				depth--
				continue
			}
			if max > 0 && commas >= max {
				return max - 1
			}
			return commas
		case t.Text == ",":
			if depth == 0 {
				commas++
			}
		}
	}
	return commas
}
