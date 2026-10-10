package semantic

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/lexer"
)

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

// A file that declares something without a `package` clause is an error at
// its first declaration, and joins no package.
func TestFileWithoutPackageIsAnError(t *testing.T) {
	files := parseFileMap(t, map[string]string{
		"test0.craftgo": `package app
type A { id string }`,
		"test1.craftgo": `

type B { id string }`,
	})
	proj, diags := AnalyzeProject(files, Options{})
	d := findCode(diags, CodePackageMissing)
	if d == nil {
		t.Fatalf("want %s, got %v", CodePackageMissing, diags)
	}
	if d.Pos.Filename != "test1.craftgo" || d.Pos.Line != 3 || d.Pos.Column != 1 {
		t.Errorf("reported at %s, want test1.craftgo:3:1", d.Pos)
	}
	expectMessage(t, d, "package <name>")
	if len(diags) != 1 {
		t.Errorf("want the one diagnostic, got %v", diags)
	}
	if got := slices.Sorted(maps.Keys(proj.Packages)); len(got) != 1 || got[0] != "app" {
		t.Errorf("packages = %v, want [app]", got)
	}
	if proj.Packages["app"].Types["B"] != nil {
		t.Error("the file without a package joined package app")
	}
}

// A package name the generated Go cannot use is an error at every file's
// `package` clause, naming why.
func TestPackageNameGoCannotUse(t *testing.T) {
	for name, why := range map[string]string{
		"func":   "Go keyword",
		"range":  "Go keyword",
		"int":    "predeclared",
		"string": "predeclared",
		"len":    "predeclared",
		"nil":    "predeclared",
		"append": "predeclared",
		"iota":   "predeclared",
		"main":   "program",
		"init":   "init",
		"_":      "blank identifier",
	} {
		files := parseFileMap(t, map[string]string{
			"a.craftgo": "package " + name + "\ntype A { id string }",
			"b.craftgo": "package " + name + "\ntype B { id string }",
		})
		_, diags := AnalyzeProject(files, Options{})
		var got []string
		for _, d := range diags {
			if d.Code != CodePackageName {
				continue
			}
			got = append(got, d.Pos.Filename)
			if d.Severity != lexer.SeverityError {
				t.Errorf("%s: severity %v, want error", name, d.Severity)
			}
			expectMessage(t, &d, `package name "`+name+`"`, why)
		}
		if len(got) != 2 {
			t.Errorf("%s: want a %s error at each clause, got %v", name, CodePackageName, diags)
		}
	}
	for _, name := range []string{"app", "Shared", "v1", "types"} {
		files := parseFileMap(t, map[string]string{"a.craftgo": "package " + name + "\ntype A { id string }"})
		_, diags := AnalyzeProject(files, Options{})
		expectNoDiags(t, diags)
	}
}

// A file that declares nothing needs no `package` clause.
func TestEmptyFileNeedsNoPackage(t *testing.T) {
	files := parseFileMap(t, map[string]string{
		"a.craftgo":     "package app\ntype A { id string }",
		"notes.craftgo": "// notes\n",
		"empty.craftgo": "",
	})
	_, diags := AnalyzeProject(files, Options{})
	expectNoDiags(t, diags)
}

func TestAnalyzeProjectCrossPackageRef(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"api.craftgo": `package design
type Login { user shared.User }`,
		"shared/user.craftgo": `package shared
type User { id string }`,
	})
	proj, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if proj.Packages["design"] == nil || proj.Packages["shared"] == nil {
		t.Errorf("expected packages design + shared, got %v", slices.Sorted(maps.Keys(proj.Packages)))
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
		t.Errorf("expected one merged package, got %d: %v", len(proj.Packages), slices.Sorted(maps.Keys(proj.Packages)))
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

// A valid @default on a cross-package scalar is accepted.
func TestAnalyzeProjectDefaultCrossPkgScalarOK(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/scalars.craftgo": `package shared
scalar CurrencyCode string @length(3, 3) @pattern("^[A-Z]{3}$")`,
		"orders/types.craftgo": `package orders
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
type Order { currency shared.CurrencyCode? @default(42) }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeDecoratorArgType) == nil {
		t.Fatalf("expected %s for int literal on string scalar, got: %v", CodeDecoratorArgType, diags)
	}
}

// A @default naming a value of a cross-package enum is accepted.
func TestAnalyzeProjectDefaultCrossPkgEnumOK(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/enums.craftgo": `package shared
enum Tier { Free Pro Enterprise }`,
		"customers/types.craftgo": `package customers
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
type Customer { tier shared.Tier? @default(Ultimate) }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeDecoratorArgValue) == nil {
		t.Fatalf("expected %s for unknown enum value, got: %v", CodeDecoratorArgValue, diags)
	}
}

// A @default on a field of a cross-package struct type is rejected.
func TestAnalyzeProjectDefaultCrossPkgUnsupportedTarget(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/types.craftgo": `package shared
type Bag { id string }`,
		"orders/types.craftgo": `package orders
type Order { bag shared.Bag? @default("nope") }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeDecoratorConflict) == nil {
		t.Fatalf("expected %s for @default on struct cross-pkg ref, got: %v", CodeDecoratorConflict, diags)
	}
}

// An array @default whose elements match a cross-package scalar's primitive is accepted.
func TestAnalyzeProjectDefaultCrossPkgArrayElement(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/scalars.craftgo": `package shared
scalar CurrencyCode string @length(3, 3)`,
		"orders/types.craftgo": `package orders
type Order { allowed shared.CurrencyCode[]? @default(["USD", "EUR"]) }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("expected no diags for cross-pkg scalar array @default, got: %v", diags)
	}
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
type Order {
	shared.Timestamps
	createdAt string
}`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeMixinConflict) == nil {
		t.Fatalf("expected %s for cross-pkg field collision, got: %v", CodeMixinConflict, diags)
	}
}

// Embedding a cross-package enum as a mixin is rejected.
func TestAnalyzeProjectMixinCrossPkgNonType(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/enums.craftgo": `package shared
enum Color { Red Blue }`,
		"orders/types.craftgo": `package orders
type Order { shared.Color }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeMixinNonType) == nil {
		t.Fatalf("expected %s for cross-pkg enum mixin, got: %v", CodeMixinNonType, diags)
	}
}

// A mixin cycle across packages (orders.A → shared.B → orders.A) is reported.
func TestAnalyzeProjectMixinCrossPkgCycle(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/mixins.craftgo": `package shared
type B { orders.A }`,
		"orders/types.craftgo": `package orders
type A { shared.B }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeMixinCycle) == nil {
		t.Fatalf("expected %s for cross-pkg mixin cycle, got: %v", CodeMixinCycle, diags)
	}
}

// A cross-package mixin naming a symbol its package does not declare is reported.
func TestAnalyzeProjectMixinCrossPkgUnresolved(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/scalars.craftgo": `package shared
scalar X string`,
		"orders/types.craftgo": `package orders
type Order { shared.MissingType }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeRefUnknownSymbol) == nil {
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
