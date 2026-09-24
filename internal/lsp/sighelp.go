package lsp

import (
	"context"
	"encoding/json"
	"strings"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// onSignatureHelp answers `textDocument/signatureHelp` inside the argument
// list of a registered decorator, and with null elsewhere.
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

// decoratorSignatureLabel renders `@name(p1, p2)`, `@name(kind...)` for a
// variadic, or `@name` without arguments, and returns the parameter labels.
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

// argKindName returns the signature label of an argument kind.
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

// activeParamIndex counts the commas between the enclosing `(` and the cursor,
// capped at max-1.
func activeParamIndex(view snapshotView, pos protocol.Position, max int) int {
	idx, _ := view.tokenAt(pos.Line, pos.Character)
	if idx < 0 {
		idx = len(view.tokens)
	}
	commas := 0
	depth := 0
	for i := idx - 1; i >= 0; i-- {
		t := view.tokens[i]
		switch t.Text {
		case ")":
			depth++
		case "(":
			if depth > 0 {
				depth--
				continue
			}
			if max > 0 && commas >= max {
				return max - 1
			}
			return commas
		case ",":
			if depth == 0 {
				commas++
			}
		}
	}
	return commas
}
