package lsp

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/parser"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// declSiteFixtures holds, per declaration keyword, a design with `@D` on the
// declaration and one with `@D` on a member of its body ("" when there is no
// such site). The declaration under test is the file's first.
var declSiteFixtures = map[lexer.Kind]struct{ self, member string }{
	lexer.KwType:       {"@D\ntype T { a string }\n", "type T {\n\t@D\n\ta string\n}\n"},
	lexer.KwEnum:       {"@D\nenum E { A }\n", "enum E {\n\tA @D\n}\n"},
	lexer.KwError:      {"@D\nerror NotFound E { a string }\n", "error NotFound E {\n\t@D\n\ta string\n}\n"},
	lexer.KwScalar:     {"scalar S string @D\n", ""},
	lexer.KwMiddleware: {"@D\nmiddleware M\n", ""},
	lexer.KwService:    {"@D\nservice S { get G /g {} }\n", "service S {\n\t@D\n\tget G /g {}\n}\n"},
	// An `extend service` takes the method decorators its blocks share plus
	// @group, which decorator completion narrows the service level to.
	lexer.KwExtend: {"", "service S { get A /a {} }\nextend service S {\n\t@D\n\tget G /g {}\n}\n"},
	lexer.KwEvent:  {"@D\nevent V { payload P }\ntype P { a string }\n", "event V {\n\t@D\n\tpayload P\n}\ntype P { a string }\n"},
}

// declSites names every declaration kind, and each of its levels is where
// semantic places decorators: one there is misplaced exactly when its levels
// miss the table's, and every one is refused at a site of level 0.
func TestDeclSitesMatchSemanticPlacement(t *testing.T) {
	kinds := map[string]bool{}
	for _, kw := range slices.Sorted(maps.Keys(declSites)) {
		site := declSites[kw]
		fx, ok := declSiteFixtures[kw]
		if !ok {
			t.Fatalf("no fixture for %s", kw)
		}
		for _, c := range []struct {
			fixture string
			level   semantic.Level
		}{{fx.self, site.self}, {fx.member, site.member}} {
			if c.fixture == "" {
				continue
			}
			bare := parseDesign(t, strings.Replace(c.fixture, "@D", "", 1))
			if len(bare.errs) > 0 {
				t.Fatalf("%s fixture without a decorator: %v", kw, bare.errs)
			}
			kinds[fmt.Sprintf("%T", bare.file.Decls[0])] = true
			for _, name := range slices.Sorted(maps.Keys(semantic.Registry)) {
				got := parseDesign(t, strings.Replace(c.fixture, "@D", "@"+name, 1)).misplaced
				want := semantic.Registry[name].Levels&c.level == 0
				if got != want {
					t.Errorf("%s at level %q: @%s misplaced = %v, want %v", kw, c.level, name, got, want)
				}
			}
		}
	}
	for _, d := range ast.AllDeclKinds() {
		if !kinds[fmt.Sprintf("%T", d)] {
			t.Errorf("no declaration keyword in declSites yields %T", d)
		}
	}
}

// A reserved word among a decorator's arguments is an argument: the site of
// `@` stays the declaration's that follows the chain.
func TestDecoratorArgumentsNeverOpenADeclaration(t *testing.T) {
	for _, c := range []struct {
		label, src   string
		want, banned []string
	}{
		{"above a chain", "package r\n\n@|\n@tags(error)\nservice S {\n\tget G /g {}\n}\n", []string{"prefix", "group", "middlewares"}, nil},
		{"editing the name", "package r\n\n@mu|tuallyExclusive(event, payload)\ntype W {\n\tevent string?\n\tpayload string?\n}\n", []string{"mutuallyExclusive"}, nil},
		{"above an extend", "package r\n\nservice S { get A /a {} }\n\n@|\n@tags(type)\nextend service S {\n\tget G /g {}\n}\n", []string{"group", "tags"}, []string{"mutuallyExclusive", "prefix"}},
		{"above a package argument", "package r\n\n@|\n@requiresOneOf(package)\ntype V { package string? }\n", []string{"requiresOneOf"}, []string{"version"}},
		{"after a scalar argument", "package r\n\n@requiresOneOf(scalar) @| @doc(\"x\")\ntype V { scalar string? }\n", []string{"requiresOneOf"}, []string{"length", "format"}},
	} {
		t.Run(c.label, func(t *testing.T) {
			items := mustCompletionsAtCursor(t, "t.craftgo", c.src)
			expectLabels(t, items, c.want...)
			expectNoLabels(t, items, c.banned...)
		})
	}
}

// On a one-line type body `@` offers the decorators of the field before it.
func TestDecoratorsOnAOneLineBodyFollowTheFieldBeforeTheCursor(t *testing.T) {
	items := mustCompletionsAtCursor(t, "t.craftgo", "package r\n\ntype Mini { a string  b int @| }\n")
	expectLabels(t, items, "gt", "range", "doc")
	expectNoLabels(t, items, "minLength", "pattern", "mutuallyExclusive")
}

// design is a parsed and analysed fixture.
type design struct {
	file      *ast.File
	errs      []string
	misplaced bool // the parser or the placement check refused a decorator
}

// parseDesign parses and analyses src as the only file of package x.
func parseDesign(t *testing.T, src string) design {
	t.Helper()
	p := parser.New("t.craftgo", "package x\n"+src)
	d := design{file: p.Parse()}
	for _, diag := range p.Diagnostics() {
		d.errs = append(d.errs, diag.Msg)
		d.misplaced = true
	}
	_, diags := semantic.Analyze([]*ast.File{d.file})
	for _, diag := range diags {
		if diag.IsError() {
			d.errs = append(d.errs, diag.Code+": "+diag.Msg)
		}
		if diag.Code == semantic.CodeDecoratorPlacement {
			d.misplaced = true
		}
	}
	return d
}
