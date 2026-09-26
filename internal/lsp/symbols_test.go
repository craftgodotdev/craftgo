package lsp

import (
	"strings"
	"testing"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// Half-typed declarations panic neither the outline nor the analyser.
func TestSemanticSurvivesPartialEditsViaSnapshot(t *testing.T) {
	cases := []string{
		"package x\nextend ",
		"package x\nextend service ",
		"package x\nextend service S ",
		"package x\nservice ",
		"package x\nservice S {\n  get  /a {}\n}",
		"package x\ntype ",
		"package x\nenum ",
		"package x\nerror NotFound ",
		"package x\nerror ",
		"package x\nscalar ",
		"package x\nmiddleware ",
	}
	for _, src := range cases {
		t.Run(src, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("snapshot pipeline panicked on partial input: %v", r)
				}
			}()
			view := parseSnapshot("t.craftgo", src)
			_ = documentSymbols(view)
			// The analyser runs on the same partial AST.
			if view.file != nil {
				_, _ = semantic.Analyze([]*ast.File{view.file})
			}
		})
	}
}

// The outline has no symbol with an empty name, which VS Code rejects.
func TestDocumentSymbolsSkipUnnamedDecls(t *testing.T) {
	cases := []struct {
		label string
		src   string
	}{
		{"bare service keyword", "package x\n\nservice "},
		{"bare type keyword", "package x\n\ntype "},
		{"bare enum keyword", "package x\n\nenum "},
		{"bare error category", "package x\n\nerror NotFound "},
		{"empty field row in type", "package x\n\ntype T {\n  \n}\n"},
		{"empty method row in service", "package x\n\nservice S {\n  get  /a {}\n}\n"},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			view := parseSnapshot("t.craftgo", c.src)
			syms := documentSymbols(view)
			for _, s := range syms {
				if s.Name == "" {
					t.Errorf("top-level symbol with empty name: %+v", s)
				}
				for _, child := range s.Children {
					if child.Name == "" {
						t.Errorf("child symbol with empty name (parent %q): %+v", s.Name, child)
					}
				}
			}
		})
	}
}

// The outline has one symbol per declaration, with its kind and a type's
// fields as children.
func TestDocumentSymbolsOutline(t *testing.T) {
	view := parseSnapshot("t.craftgo", testDSL)
	syms := documentSymbols(view)
	want := map[string]protocol.SymbolKind{
		"Greeter":        protocol.SymbolKindStruct,
		"Status":         protocol.SymbolKindEnum,
		"GreeterService": protocol.SymbolKindInterface,
	}
	got := make(map[string]protocol.SymbolKind, len(syms))
	for _, s := range syms {
		got[s.Name] = s.Kind
	}
	for name, kind := range want {
		if got[name] != kind {
			t.Errorf("symbol %q: kind = %v, want %v", name, got[name], kind)
		}
	}
	for _, s := range syms {
		if s.Name == "Greeter" && len(s.Children) < 2 {
			t.Errorf("Greeter should have >=2 field children, got %d", len(s.Children))
		}
	}
}

// A declaration of every kind has one symbol kind, in the outline and in the
// workspace symbols alike.
func TestSymbolKindsAgreeAcrossViews(t *testing.T) {
	src := "package x\ntype T { a string }\nenum E { A }\nerror NotFound Err\nscalar S string\n" +
		"middleware M\nservice Svc { get G /g {} }\nevent Ev { payload T }\n"
	u := uri.New("file:///t.craftgo")
	s := &server{docs: map[uri.URI]string{u: src}}
	res, _ := callHandler(t, s, protocol.MethodTextDocumentDocumentSymbol, protocol.DocumentSymbolParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: u},
	})
	outline := map[string]protocol.SymbolKind{}
	for _, sym := range res.([]protocol.DocumentSymbol) {
		outline[sym.Name] = sym.Kind
	}
	if len(outline) != len(ast.AllDeclKinds()) {
		t.Fatalf("outline = %v, want one symbol per declaration kind", outline)
	}
	res, _ = callHandler(t, s, protocol.MethodWorkspaceSymbol, protocol.WorkspaceSymbolParams{})
	for _, sym := range res.([]protocol.SymbolInformation) {
		if outline[sym.Name] != sym.Kind {
			t.Errorf("%s: outline kind %v, workspace kind %v", sym.Name, outline[sym.Name], sym.Kind)
		}
	}
}

// infoOf gives every declaration kind a declaration line naming it, a symbol
// kind and a completion item kind.
func TestInfoOfCoversEveryDeclKind(t *testing.T) {
	for _, d := range ast.AllDeclKinds() {
		info := infoOf(d)
		if !strings.Contains(info.summary, " "+d.DeclName()) || info.symbol == 0 || info.item == 0 {
			t.Errorf("%T: %+v", d, info)
		}
	}
}
