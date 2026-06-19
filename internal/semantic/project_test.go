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

// projectFixture writes the supplied src map to a temp dir under
// designRoot and returns (designRoot, []*ast.File). Keys are
// design-relative paths (`api.craftgo`, `shared/user.craftgo`); values
// are the file contents. Parser diagnostics fail the test.
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

// ---------- single-package fallback ----------

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

// TestAnalyzeProjectEmptyRootNoPackageDecl covers the fallback when
// the single-package analysis returns a package without a `package X`
// keyword - the project keys it under "" instead of by name.
func TestAnalyzeProjectEmptyRootNoPackageDecl(t *testing.T) {
	files := parseFiles(t, `type X { id string }`)
	proj, _ := AnalyzeProject(files, Options{})
	if _, ok := proj.Packages[""]; !ok {
		t.Errorf("expected fallback empty key, got %v", pkgNames(proj))
	}
}

// ---------- happy path: cross-package ref ----------

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

// ---------- merge: same-package files in different folders ----------

func TestAnalyzeProjectFoldermergeStillWorks(t *testing.T) {
	// All files declare `package design` - they merge into one
	// package regardless of folder location, matching the existing
	// `import = pull files from folder into my package` semantics.
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

// ---------- import errors ----------

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

func TestAnalyzeProjectImportSelfWarning(t *testing.T) {
	// `package shared` + import "shared" - the import resolves to a
	// folder whose files share this package name, so the import is
	// a no-op. Surfaced as a warning, not an error.
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

// ---------- ref errors ----------

func TestAnalyzeProjectUnknownPackage(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"api.craftgo": `package design
type X { user shared.User }`,
		"shared/user.craftgo": `package shared
type User { id string }`,
	})
	// shared.User in api.craftgo resolves correctly because package
	// "shared" exists. Use a name that does NOT exist to trigger the
	// unknown-package path.
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

// ---------- cross-pkg via mixin / generic ----------

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

// ---------- helpers ----------

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

func TestPackageHasSymbol(t *testing.T) {
	pkg := &Package{
		Types:   map[string]*ast.TypeDecl{"T": {}},
		Enums:   map[string]*ast.EnumDecl{"E": {}},
		Errors:  map[string]*ast.ErrorDecl{"R": {}},
		Scalars: map[string]*ast.ScalarDecl{"S": {}},
	}
	for _, name := range []string{"T", "E", "R", "S"} {
		if !packageHasSymbol(pkg, name) {
			t.Errorf("expected %q to be present", name)
		}
	}
	if packageHasSymbol(pkg, "X") {
		t.Error("X should not be present")
	}
	if packageHasSymbol(nil, "T") {
		t.Error("nil pkg should return false")
	}
}

func TestFileFilenameFallback(t *testing.T) {
	if got := fileFilename(nil); got != "" {
		t.Error("nil should be empty")
	}
	if got := fileFilename(&ast.File{}); got != "" {
		t.Error("empty file should be empty")
	}
	got := fileFilename(&ast.File{
		Decls: []ast.Decl{
			&ast.TypeDecl{Pos: lexer.Position{Filename: "from-decl.craftgo", Line: 1}, Name: "X"},
		},
	})
	if got != "from-decl.craftgo" {
		t.Errorf("decl-fallback got %q", got)
	}
}

func TestFilePosFallback(t *testing.T) {
	got := filePos(&ast.File{})
	if got.Line != 1 {
		t.Errorf("empty file should fallback to line 1, got %v", got)
	}
	if got := filePos(nil); got.Line != 0 {
		t.Errorf("nil should be zero pos, got %v", got)
	}
	got = filePos(&ast.File{
		Decls: []ast.Decl{
			&ast.TypeDecl{Pos: lexer.Position{Line: 42}, Name: "X"},
		},
	})
	if got.Line != 42 {
		t.Errorf("decl fallback got %v", got)
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
	// Folder exists but no .craftgo file inside.
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if folderExists(root, "empty") {
		t.Error("folder without .craftgo files should report false")
	}
	// Folder with a .craftgo file.
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

	// Folder is unreadable: ReadDir fails, function returns false.
	noPerm := filepath.Join(root, "noperm")
	if err := os.MkdirAll(noPerm, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(noPerm, 0o755) })
	if folderExists(root, "noperm") {
		t.Error("unreadable folder should report false")
	}
}

// TestFilePosFromPackage covers the f.Package != nil branch (mirrors
// the decl-fallback test below).
func TestFilePosFromPackage(t *testing.T) {
	got := filePos(&ast.File{Package: &ast.PackageDecl{Pos: lexer.Position{Line: 7}, Name: "x"}})
	if got.Line != 7 {
		t.Errorf("expected line 7, got %v", got)
	}
}

func TestProcessFileNilTolerated(t *testing.T) {
	r := &refResolver{proj: &Project{Packages: map[string]*Package{}, FileImports: map[string]map[string]string{}}}
	r.processFile(nil, "/root")
	if len(r.diags) != 0 {
		t.Errorf("nil file should not diag, got %v", r.diags)
	}
}

func TestWalkRefNilGuards(t *testing.T) {
	r := &refResolver{proj: &Project{Packages: map[string]*Package{}}}
	r.walkTypeRef(nil, "")
	r.walkNamedRef(nil, "")
	r.walkNamedRef(&ast.NamedTypeRef{}, "")
	if len(r.diags) != 0 {
		t.Errorf("nil refs should not diag, got %v", r.diags)
	}
}

// TestProcessFileEmptyPathSkipped exercises the `path == ""` skip in
// resolveImports. Parser doesn't normally emit empty paths but the
// guard exists for malformed input.
func TestProcessFileEmptyPathSkipped(t *testing.T) {
	r := &refResolver{proj: &Project{Packages: map[string]*Package{}, FileImports: map[string]map[string]string{}}}
	f := &ast.File{Imports: []*ast.Import{
		{Pos: lexer.Position{Line: 1}, Path: ""},
	}}
	r.processFile(f, "")
	if len(r.diags) != 0 {
		t.Errorf("empty import path should not diag, got %v", r.diags)
	}
}

// TestWalkDeclRefsCoversErrorAndService drives walkDeclRefs through
// the ErrorDecl and ServiceDecl branches that AnalyzeProject's
// happy-path tests don't always reach.
func TestWalkDeclRefsCoversErrorAndService(t *testing.T) {
	r := &refResolver{proj: &Project{
		Packages: map[string]*Package{"design": {Types: map[string]*ast.TypeDecl{"User": {}}}},
	}}
	// Error body referencing a multi-part name.
	r.walkDeclRefs(&ast.ErrorDecl{
		Body: []ast.TypeMember{
			&ast.Field{Type: &ast.TypeRef{Named: &ast.NamedTypeRef{
				Name: &ast.QualifiedIdent{Parts: []string{"design", "User"}},
			}}},
		},
	}, "")
	// Service with method req+resp.
	r.walkDeclRefs(&ast.ServiceDecl{Members: []ast.ServiceMember{
		&ast.Method{
			Request:  &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"design", "User"}}},
			Response: &ast.MethodResponse{Type: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"design", "User"}}}},
		},
		// Method with no request/response - both nil branches.
		&ast.Method{},
	}}, "")
	if len(r.diags) != 0 {
		t.Errorf("happy-path multi-part refs should resolve, got %v", r.diags)
	}
}

func TestWalkNamedRefMapBranch(t *testing.T) {
	r := &refResolver{proj: &Project{Packages: map[string]*Package{}}}
	r.walkTypeRef(&ast.TypeRef{Map: &ast.MapType{
		Key:   &ast.TypeRef{Named: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"string"}}}},
		Value: &ast.TypeRef{Named: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"string"}}}},
	}}, "")
	if len(r.diags) != 0 {
		t.Errorf("map of unqualified should not diag, got %v", r.diags)
	}
}

// pkgNames returns the sorted package keys of proj for test
// diagnostics.
func pkgNames(p *Project) []string {
	out := make([]string, 0, len(p.Packages))
	for k := range p.Packages {
		out = append(out, k)
	}
	return out
}

// ---------- @default on cross-package scalar / enum (project-level) ----------

// TestAnalyzeProjectDefaultCrossPkgScalarOK covers the happy path:
// `currency shared.CurrencyCode? @default("USD")` where the scalar
// lives in a different package than the field. The per-package pass
// defers the check; the project pass resolves the qualified ref and
// validates the literal kind.
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

// TestAnalyzeProjectDefaultCrossPkgScalarKindMismatch surfaces the kind
// check that the per-package pass cannot run (lacks cross-pkg view).
// `@default(42)` on a string-backed scalar must fire @default's argtype
// diagnostic at project time.
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

// TestAnalyzeProjectDefaultCrossPkgEnumOK covers @default(EnumValue) on a
// cross-package enum field - the project pass walks the enum's value
// set the same way the per-package pass would for local enums.
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

// TestAnalyzeProjectDefaultCrossPkgEnumUnknownValue catches the
// "expected one of X, Y, Z" path for cross-package enums.
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

// TestAnalyzeProjectDefaultCrossPkgUnsupportedTarget covers the
// "you pointed @default at a struct type" case across package
// boundaries - the cross-pkg ref resolves to a *type*, not a scalar
// or enum, so @default should still fire decorator/conflict.
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

// TestAnalyzeProjectDefaultCrossPkgArrayElement covers an array field
// whose ELEMENT type is a qualified scalar: `methods shared.CurrencyCode[]?
// @default(["USD", "EUR"])` must validate each element against the
// scalar's underlying primitive kind (string).
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

// hasCode is a tiny test helper for "did any diagnostic come back with
// this code?" - keeps assertions readable without depending on order
// or auxiliary messages.
func hasCode(diags []Diagnostic, code string) bool {
	for _, d := range diags {
		if d.Code == code {
			return true
		}
	}
	return false
}

// ---------- cross-package mixin ----------

// TestAnalyzeProjectMixinCrossPkgOK covers the happy path: a type in
// one package embeds a mixin declared in another. The expanded fields
// land in the host's effective field set and the codegen sees them.
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

// TestAnalyzeProjectMixinCrossPkgConflict pins the conflict path:
// a host's direct field collides with a field a cross-pkg mixin
// would bring in.
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

// TestAnalyzeProjectMixinCrossPkgNonType covers the type-vs-other
// kind check across packages. `shared.Color` is an enum; mixin'ing it
// must fire the per-pkg-style "is a enum, not a type" diagnostic at
// project time.
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

// TestAnalyzeProjectMixinCrossPkgCycle covers a cycle that crosses a
// package boundary: orders.A → shared.B → orders.A. The unified
// visited map keys by qualified name so the cycle terminates with the
// usual diagnostic instead of recursing forever.
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

// TestAnalyzeProjectMixinCrossPkgUnresolved fires when the qualified
// prefix resolves to a known package but the symbol isn't declared.
// The per-package pass cannot tell ("symbol might live elsewhere") so
// this responsibility falls to the project resolver.
func TestAnalyzeProjectMixinCrossPkgUnresolved(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/scalars.craftgo": `package shared
scalar X string`,
		"orders/types.craftgo": `package orders
import "shared"
type Order { shared.MissingType }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if !hasCode(diags, CodeMixinUnresolved) {
		t.Fatalf("expected %s for unresolved cross-pkg mixin, got: %v", CodeMixinUnresolved, diags)
	}
}
