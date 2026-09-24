package semantic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/parser"
)

// projectFixture writes src (design-relative path → content) under a temp root and parses it.
func projectFixture(t *testing.T, src map[string]string) (string, []*ast.File) {
	t.Helper()
	root := t.TempDir()
	var files []*ast.File
	for rel, content := range src {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		p := parser.New(full, content)
		f := p.Parse()
		if d := p.Diagnostics(); len(d) > 0 {
			t.Fatalf("parse error in %s: %v", rel, d)
		}
		files = append(files, f)
	}
	return root, files
}

func TestAnalyzeProjectEmptyRootDelegates(t *testing.T) {
	files := parseFiles(t, `package design
type X { id string }`)
	proj, diags := AnalyzeProject(files, Options{})
	if len(diags) > 0 {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if len(proj.Packages) != 1 {
		t.Errorf("expected single package, got %d", len(proj.Packages))
	}
	if proj.Packages["design"] == nil {
		t.Error("expected package 'design'")
	}
}

// Without a design root, a package with no `package` clause is keyed under "".
func TestAnalyzeProjectEmptyRootNoPackageDecl(t *testing.T) {
	files := parseFiles(t, `type X { id string }`)
	proj, _ := AnalyzeProject(files, Options{})
	if _, ok := proj.Packages[""]; !ok {
		t.Errorf("expected fallback empty key, got %v", pkgNames(proj))
	}
}

func TestAnalyzeProjectCrossPackageRef(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"api.craftgo": `package design
import "shared"
type Login { user shared.User }`,
		"shared/user.craftgo": `package shared
type User { id string }`,
	})
	proj, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if proj.Packages["design"] == nil || proj.Packages["shared"] == nil {
		t.Errorf("expected packages design + shared, got %v", pkgNames(proj))
	}
}

// Files declaring the same package merge into one, whatever their folder.
func TestAnalyzeProjectFoldermergeStillWorks(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"services.craftgo": `package design
type Local { x string }`,
		"shared/contracts/types.craftgo": `package design
type Pong { name string }`,
	})
	proj, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if len(proj.Packages) != 1 {
		t.Errorf("expected one merged package, got %d: %v", len(proj.Packages), pkgNames(proj))
	}
	pkg := proj.Packages["design"]
	if pkg == nil {
		t.Fatal("missing package 'design'")
	}
	if _, ok := pkg.Types["Local"]; !ok {
		t.Error("Local missing from merged package")
	}
	if _, ok := pkg.Types["Pong"]; !ok {
		t.Error("Pong (from subfolder) missing from merged package")
	}
}

func TestAnalyzeProjectImportUnresolved(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"api.craftgo": `package design
import "missing"
type X { id string }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeImportUnresolved) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestAnalyzeProjectImportEscapeAbsolute(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"api.craftgo": `package design
import "/etc/passwd"
type X { id string }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeImportEscape) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestAnalyzeProjectImportEscapeDotDot(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"api.craftgo": `package design
import "../outside"
type X { id string }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeImportEscape) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

// Importing the folder of the file's own package is a no-op that warns.
func TestAnalyzeProjectImportSelfWarning(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/a.craftgo": `package shared
import "shared"
type A { id string }`,
		"shared/b.craftgo": `package shared
type B { id string }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	d := findCode(diags, CodeImportSelf)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if d.Severity != lexer.SeverityWarning {
		t.Errorf("expected warning, got %v", d.Severity)
	}
}

func TestAnalyzeProjectUnknownPackage(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"api.craftgo": `package design
type X { user shared.User }`,
		"shared/user.craftgo": `package shared
type User { id string }`,
	})
	// shared.User resolves because package shared exists; mystery.Thing does not.
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeRefUnknownPackage) != nil {
		t.Fatalf("happy path should resolve, got %v", codes(diags))
	}

	root2, files2 := projectFixture(t, map[string]string{
		"api.craftgo": `package design
type X { user mystery.Thing }`,
	})
	_, diags2 := AnalyzeProject(files2, Options{DesignRoot: root2})
	if findCode(diags2, CodeRefUnknownPackage) == nil {
		t.Fatalf("expected unknown-package, got %v", codes(diags2))
	}
}

func TestAnalyzeProjectUnknownSymbol(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"api.craftgo": `package design
import "shared"
type X { user shared.Mystery }`,
		"shared/user.craftgo": `package shared
type User { id string }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	d := findCode(diags, CodeRefUnknownSymbol)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "Mystery") {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestAnalyzeProjectQualifiedTooManySegments(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"api.craftgo": `package design
type X { user shared.deep.User }`,
		"shared/user.craftgo": `package shared
type User { id string }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeQualifiedRef) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestAnalyzeProjectCrossPackageMixin(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"api.craftgo": `package design
import "shared"
type User { shared.Auditable  name string }`,
		"shared/auditable.craftgo": `package shared
type Auditable { createdAt string }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeRefUnknownPackage) != nil {
		t.Fatalf("cross-pkg mixin should resolve, got %v", codes(diags))
	}
	if findCode(diags, CodeRefUnknownSymbol) != nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestAnalyzeProjectCrossPackageGenericArg(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"api.craftgo": `package design
import "shared"
type Page<T> { items T[] }
type Listing { p Page<shared.User> }`,
		"shared/user.craftgo": `package shared
type User { id string }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeRefUnknownPackage) != nil {
		t.Fatalf("cross-pkg generic arg should resolve, got %v", codes(diags))
	}
	if findCode(diags, CodeRefUnknownSymbol) != nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestLastSegment(t *testing.T) {
	cases := map[string]string{
		"":           "",
		"shared":     "shared",
		"auth/types": "types",
		"a/b/c":      "c",
	}
	for in, want := range cases {
		if got := idents.LastSegment(in); got != want {
			t.Errorf("idents.LastSegment(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsEscapingPath(t *testing.T) {
	cases := map[string]bool{
		"":           false,
		"shared":     false,
		"auth/types": false,
		"/etc":       true,
		"./shared":   true,
		"../up":      true,
		"..":         true,
		".":          true,
	}
	for in, want := range cases {
		if got := isEscapingPath(in); got != want {
			t.Errorf("isEscapingPath(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestFolderExists(t *testing.T) {
	root := t.TempDir()
	if folderExists(root, "missing") {
		t.Error("missing folder should report false")
	}
	if folderExists("", "shared") {
		t.Error("empty root should report false")
	}
	if folderExists(root, "") {
		t.Error("empty path should report false")
	}
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if folderExists(root, "empty") {
		t.Error("folder without .craftgo files should report false")
	}
	target := filepath.Join(root, "shared", "user.craftgo")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("package x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !folderExists(root, "shared") {
		t.Error("populated folder should report true")
	}

	noPerm := filepath.Join(root, "noperm")
	if err := os.MkdirAll(noPerm, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(noPerm, 0o755) })
	if folderExists(root, "noperm") {
		t.Error("unreadable folder should report false")
	}
}

func TestProcessFileNilTolerated(t *testing.T) {
	r := &refResolver{proj: &Project{Packages: map[string]*Package{}}}
	r.processFile(nil, "/root")
	if len(r.diags) != 0 {
		t.Errorf("nil file should not diag, got %v", r.diags)
	}
}

func TestCheckQualifiedRefNamelessRef(t *testing.T) {
	r := &refResolver{proj: &Project{Packages: map[string]*Package{}}}
	r.checkQualifiedRef(&ast.NamedTypeRef{}, "")
	if len(r.diags) != 0 {
		t.Errorf("nameless ref should not diag, got %v", r.diags)
	}
}

// An import with an empty path is skipped without a diagnostic.
func TestProcessFileEmptyPathSkipped(t *testing.T) {
	r := &refResolver{proj: &Project{Packages: map[string]*Package{}}}
	f := &ast.File{Imports: []*ast.Import{
		{Pos: lexer.Position{Line: 1}, Path: ""},
	}}
	r.processFile(f, "")
	if len(r.diags) != 0 {
		t.Errorf("empty import path should not diag, got %v", r.diags)
	}
}

// walkDeclRefs resolves qualified refs in error bodies and in method requests and responses.
func TestWalkDeclRefsCoversErrorAndService(t *testing.T) {
	r := &refResolver{proj: &Project{
		Packages: map[string]*Package{"design": {Types: map[string]*ast.TypeDecl{"User": {}}}},
	}}
	r.walkDeclRefs(&ast.ErrorDecl{
		Body: []ast.TypeMember{
			&ast.Field{Type: &ast.TypeRef{Named: &ast.NamedTypeRef{
				Name: &ast.QualifiedIdent{Parts: []string{"design", "User"}},
			}}},
		},
	}, "")
	r.walkDeclRefs(&ast.ServiceDecl{Members: []ast.ServiceMember{
		&ast.Method{
			Request:  &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"design", "User"}}},
			Response: &ast.MethodResponse{Type: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"design", "User"}}}},
		},
		&ast.Method{},
	}}, "")
	if len(r.diags) != 0 {
		t.Errorf("happy-path multi-part refs should resolve, got %v", r.diags)
	}
}

func TestWalkDeclRefsMapOfUnqualified(t *testing.T) {
	r := &refResolver{proj: &Project{Packages: map[string]*Package{}}}
	r.walkDeclRefs(&ast.TypeDecl{Body: []ast.TypeMember{&ast.Field{Type: &ast.TypeRef{Map: &ast.MapType{
		Key:   &ast.TypeRef{Named: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"string"}}}},
		Value: &ast.TypeRef{Named: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"string"}}}},
	}}}}}, "")
	if len(r.diags) != 0 {
		t.Errorf("map of unqualified should not diag, got %v", r.diags)
	}
}

func pkgNames(p *Project) []string {
	out := make([]string, 0, len(p.Packages))
	for k := range p.Packages {
		out = append(out, k)
	}
	return out
}

// A valid @default on a cross-package scalar is accepted.
func TestAnalyzeProjectDefaultCrossPkgScalarOK(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/scalars.craftgo": `package shared
scalar CurrencyCode string @length(3, 3) @pattern("^[A-Z]{3}$")`,
		"orders/types.craftgo": `package orders
import "shared"
type Order { currency shared.CurrencyCode? @default("USD") }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("expected no diags for cross-pkg scalar @default, got: %v", diags)
	}
}

// A @default whose kind mismatches a cross-package scalar is rejected.
func TestAnalyzeProjectDefaultCrossPkgScalarKindMismatch(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/scalars.craftgo": `package shared
scalar CurrencyCode string @length(3, 3)`,
		"orders/types.craftgo": `package orders
import "shared"
type Order { currency shared.CurrencyCode? @default(42) }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if !hasCode(diags, CodeDecoratorArgType) {
		t.Fatalf("expected %s for int literal on string scalar, got: %v", CodeDecoratorArgType, diags)
	}
}

// A @default naming a value of a cross-package enum is accepted.
func TestAnalyzeProjectDefaultCrossPkgEnumOK(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/enums.craftgo": `package shared
enum Tier { Free Pro Enterprise }`,
		"customers/types.craftgo": `package customers
import "shared"
type Customer { tier shared.Tier? @default(Pro) }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("expected no diags for cross-pkg enum @default, got: %v", diags)
	}
}

// A @default naming no value of a cross-package enum is rejected.
func TestAnalyzeProjectDefaultCrossPkgEnumUnknownValue(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/enums.craftgo": `package shared
enum Tier { Free Pro Enterprise }`,
		"customers/types.craftgo": `package customers
import "shared"
type Customer { tier shared.Tier? @default(Ultimate) }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if !hasCode(diags, CodeDecoratorArgValue) {
		t.Fatalf("expected %s for unknown enum value, got: %v", CodeDecoratorArgValue, diags)
	}
}

// A @default on a field of a cross-package struct type is rejected.
func TestAnalyzeProjectDefaultCrossPkgUnsupportedTarget(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/types.craftgo": `package shared
type Bag { id string }`,
		"orders/types.craftgo": `package orders
import "shared"
type Order { bag shared.Bag? @default("nope") }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if !hasCode(diags, CodeDecoratorConflict) {
		t.Fatalf("expected %s for @default on struct cross-pkg ref, got: %v", CodeDecoratorConflict, diags)
	}
}

// An array @default whose elements match a cross-package scalar's primitive is accepted.
func TestAnalyzeProjectDefaultCrossPkgArrayElement(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/scalars.craftgo": `package shared
scalar CurrencyCode string @length(3, 3)`,
		"orders/types.craftgo": `package orders
import "shared"
type Order { allowed shared.CurrencyCode[]? @default(["USD", "EUR"]) }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("expected no diags for cross-pkg scalar array @default, got: %v", diags)
	}
}

// hasCode reports whether any diagnostic carries code.
func hasCode(diags []Diagnostic, code string) bool {
	for _, d := range diags {
		if d.Code == code {
			return true
		}
	}
	return false
}

// A type embeds a mixin declared in another package.
func TestAnalyzeProjectMixinCrossPkgOK(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/mixins.craftgo": `package shared
type Timestamps {
	createdAt string @format(datetime)
	updatedAt string @format(datetime)
}`,
		"orders/types.craftgo": `package orders
import "shared"
type Order {
	shared.Timestamps
	id    string
	total int
}`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("expected no diags for cross-pkg mixin, got: %v", diags)
	}
}

// A host field that collides with a cross-package mixin field conflicts.
func TestAnalyzeProjectMixinCrossPkgConflict(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/mixins.craftgo": `package shared
type Timestamps {
	createdAt string @format(datetime)
}`,
		"orders/types.craftgo": `package orders
import "shared"
type Order {
	shared.Timestamps
	createdAt string
}`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if !hasCode(diags, CodeMixinConflict) {
		t.Fatalf("expected %s for cross-pkg field collision, got: %v", CodeMixinConflict, diags)
	}
}

// Embedding a cross-package enum as a mixin is rejected.
func TestAnalyzeProjectMixinCrossPkgNonType(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/enums.craftgo": `package shared
enum Color { Red Blue }`,
		"orders/types.craftgo": `package orders
import "shared"
type Order { shared.Color }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if !hasCode(diags, CodeMixinNonType) {
		t.Fatalf("expected %s for cross-pkg enum mixin, got: %v", CodeMixinNonType, diags)
	}
}

// A mixin cycle across packages (orders.A → shared.B → orders.A) is reported.
func TestAnalyzeProjectMixinCrossPkgCycle(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/mixins.craftgo": `package shared
import "orders"
type B { orders.A }`,
		"orders/types.craftgo": `package orders
import "shared"
type A { shared.B }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if !hasCode(diags, CodeMixinCycle) {
		t.Fatalf("expected %s for cross-pkg mixin cycle, got: %v", CodeMixinCycle, diags)
	}
}

// A cross-package mixin naming a symbol its package does not declare is reported.
func TestAnalyzeProjectMixinCrossPkgUnresolved(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/scalars.craftgo": `package shared
scalar X string`,
		"orders/types.craftgo": `package orders
import "shared"
type Order { shared.MissingType }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if !hasCode(diags, CodeRefUnknownSymbol) {
		t.Fatalf("expected %s for unresolved cross-pkg mixin, got: %v", CodeRefUnknownSymbol, diags)
	}
}

// Diagnostics come back ordered by file and offset, identical on every run.
func TestAnalyzeProjectDiagnosticsSorted(t *testing.T) {
	files := parseFileMap(t, map[string]string{
		"b.craftgo": `package app
type Box<T> { v T }
type P { a Box }
type Q { b Box<string, int> }
type R { c Box<int, int, int> }
type lower { id string }`,
		"a.craftgo": `package app
type S { d Box<string, string> }
type T2 { e Missing }
type lowerToo { id string }`,
	})
	var want []Diagnostic
	for run := range 20 {
		_, diags := AnalyzeProject(files, Options{})
		for i := 1; i < len(diags); i++ {
			prev, cur := diags[i-1].Pos, diags[i].Pos
			if prev.Filename > cur.Filename || prev.Filename == cur.Filename && prev.Offset > cur.Offset {
				t.Fatalf("run %d: %s (%s) reported before %s (%s)", run, prev, diags[i-1].Code, cur, diags[i].Code)
			}
		}
		if run == 0 {
			want = diags
			continue
		}
		if len(diags) != len(want) {
			t.Fatalf("run %d: %d diagnostics, first run had %d", run, len(diags), len(want))
		}
		for i := range diags {
			if diags[i].Pos != want[i].Pos || diags[i].Code != want[i].Code || diags[i].Msg != want[i].Msg {
				t.Fatalf("run %d: diagnostic %d is %s %s, first run had %s %s", run, i, diags[i].Pos, diags[i].Code, want[i].Pos, want[i].Code)
			}
		}
	}
}
