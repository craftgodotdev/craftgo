package lsp

import (
	"context"
	"strings"

	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// onSignatureHelp answers `textDocument/signatureHelp` inside the argument
// list of a registered decorator, and with null elsewhere.
func (s *server) onSignatureHelp(_ context.Context, params protocol.SignatureHelpParams) (any, error) {
	r, ok := s.open(params.TextDocument.URI)
	if !ok {
		return nil, nil
	}
	view := r.view()
	c := view.cursorAt(params.Position)
	name, lparen, ok := decoratorArgContext(view, c)
	if !ok {
		return nil, nil
	}
	spec, ok := semantic.Lookup(name)
	if !ok {
		return nil, nil
	}
	label, paramLabels := decoratorSignatureLabel(name, spec)
	if label == "" {
		return nil, nil
	}
	paramInfos := make([]protocol.ParameterInformation, 0, len(paramLabels))
	for _, p := range paramLabels {
		paramInfos = append(paramInfos, protocol.ParameterInformation{Label: p})
	}
	active := activeParamIndex(view, c, lparen, len(paramLabels))
	sig := protocol.SignatureInformation{
		Label:           label,
		Documentation:   spec.Doc,
		Parameters:      paramInfos,
		ActiveParameter: uint32(active),
	}
	return &protocol.SignatureHelp{
		Signatures:      []protocol.SignatureInformation{sig},
		ActiveSignature: 0,
		ActiveParameter: uint32(active),
	}, nil
}

// decoratorSignatureLabel renders `@name(p1, p2)`, `@name(kind...)` for a
// variadic, or `@name` without arguments, and returns the parameter labels.
func decoratorSignatureLabel(name string, spec semantic.Spec) (string, []string) {
	rule := spec.Args
	var parts []string
	if rule.Variadic != 0 {
		parts = append(parts, rule.Variadic.String()+"...")
	} else if len(rule.Kinds) > 0 {
		for _, k := range rule.Kinds {
			parts = append(parts, k.String())
		}
	} else if rule.Min == 0 && rule.Max == 0 {
		return "@" + name, nil
	}
	if len(parts) == 0 {
		return "@" + name, nil
	}
	return "@" + name + "(" + strings.Join(parts, ", ") + ")", parts
}

// activeParamIndex counts the top-level commas after the `(` at lparen that
// start before the cursor, capped at max-1.
func activeParamIndex(view snapshotView, c cursor, lparen, max int) int {
	commas, depth, last := 0, 0, view.lastBefore(c)
	for i := lparen + 1; i <= last; i++ {
		switch view.tokens[i].Kind {
		case lexer.LParen, lexer.LBracket, lexer.LBrace:
			depth++
		case lexer.RParen, lexer.RBracket, lexer.RBrace:
			depth--
		case lexer.Comma:
			if depth == 0 {
				commas++
			}
		}
	}
	if max > 0 && commas >= max {
		return max - 1
	}
	return commas
}
