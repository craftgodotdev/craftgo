package semantic

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/parser"
	"github.com/craftgodotdev/craftgo/internal/route"
)

// parseFileMap parses src as a slice of named files and returns the AST
// set ready for [Analyze].
func parseFileMap(t *testing.T, files map[string]string) []*ast.File {
	t.Helper()
	out := make([]*ast.File, 0, len(files))
	for name, src := range files {
		p := parser.New(name, src)
		f := p.Parse()
		if d := p.Diagnostics(); len(d) > 0 {
			t.Fatalf("parse %s: %v", name, d)
		}
		out = append(out, f)
	}
	return out
}

func TestSemanticMergesMultipleFilesSamePackage(t *testing.T) {
	files := parseFileMap(t, map[string]string{
		"types.craftgo": `package design

type User { id string  name string }
type Page { total int }
`,
		"api.craftgo": `package design

import "shared"

@prefix("/v1")
service S {
    get GetUser /users/{id} {
        request   User
        response  User
    }
}

extend service S {
    get GetPage /pages {
        response  Page
    }
}
`,
	})
	pkg, diags := Analyze(files)
	if len(diags) > 0 {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if pkg.Name != "design" {
		t.Errorf("expected design, got %q", pkg.Name)
	}
	if _, ok := pkg.Types["User"]; !ok {
		t.Error("User missing from merged package")
	}
	if _, ok := pkg.Types["Page"]; !ok {
		t.Error("Page missing from merged package")
	}
	svc, ok := pkg.Services["S"]
	if !ok {
		t.Fatal("service S missing")
	}
	if len(svc.Methods) != 2 {
		t.Errorf("expected 2 merged methods, got %d", len(svc.Methods))
	}
}

func TestSemanticConflictingPackageNames(t *testing.T) {
	files := parseFileMap(t, map[string]string{
		"a.craftgo": `package design
type X { id string }
`,
		"b.craftgo": `package other
type Y { id string }
`,
	})
	_, diags := Analyze(files)
	if len(diags) == 0 {
		t.Fatal("expected diag for conflicting package names")
	}
}

func TestSemanticDuplicateTopLevelAcrossFiles(t *testing.T) {
	files := parseFileMap(t, map[string]string{
		"a.craftgo": `package design
type X { id string }
`,
		"b.craftgo": `package design
type X { name string }
`,
	})
	_, diags := Analyze(files)
	if len(diags) == 0 {
		t.Fatal("expected duplicate-decl diag")
	}
}

func TestSemanticExtendWithoutPrimary(t *testing.T) {
	files := parseFileMap(t, map[string]string{
		"a.craftgo": `package design
type R {}
extend service Missing {
    get X /x { response R }
}
`,
	})
	_, diags := Analyze(files)
	if len(diags) == 0 {
		t.Fatal("expected diag for extend without primary")
	}
}

func TestSemanticRouteCollision(t *testing.T) {
	files := parseFileMap(t, map[string]string{
		"a.craftgo": `package design
type R {}
service S {
    get A /same { response R }
    get B /same { response R }
}
`,
	})
	_, diags := Analyze(files)
	if len(diags) == 0 {
		t.Fatal("expected route-collision diag")
	}
}

func TestSemanticEnumKindsMustBeUniform(t *testing.T) {
	files := parseFileMap(t, map[string]string{
		"a.craftgo": `package design
enum Mixed {
    A = "a"
    B = 1
}
`,
	})
	_, diags := Analyze(files)
	if len(diags) == 0 {
		t.Fatal("expected mixed-kind diag")
	}
}

func TestSemanticImportsParsedNotResolved(t *testing.T) {
	// Imports are accepted at the parse level; single-package Analyze
	// does not resolve them across packages.
	files := parseFileMap(t, map[string]string{
		"a.craftgo": `package design
import "shared/types"
import alias "other"
type X { id string }
`,
	})
	pkg, diags := Analyze(files)
	if len(diags) > 0 {
		t.Fatalf("imports should not raise diags: %v", diags)
	}
	if _, ok := pkg.Types["X"]; !ok {
		t.Error("X missing")
	}
}

func TestSemanticPathStringRenders(t *testing.T) {
	files := parseFileMap(t, map[string]string{
		"a.craftgo": `package design
type Req { id string }
type Resp {}
service S {
    get H /api/v1/users/{id} {
        request Req
        response Resp
    }
}
`,
	})
	pkg, _ := Analyze(files)
	got := route.PathString(pkg.Services["S"].Methods[0].Path)
	if got != "/api/v1/users/{id}" {
		t.Errorf("PathString = %q", got)
	}
}

// TestErrorsTypoRejectedProjectMode pins that an @errors typo is
// rejected in project (multi-package) mode, where the per-package
// decorator-ref check is skipped (it defers cross-package resolution).
func TestErrorsTypoRejectedProjectMode(t *testing.T) {
	files := parseFileMap(t, map[string]string{
		"svc.craftgo": `package design

type Req { id string }
type Res { ok bool }

service S {
    @errors(RealNotFound)
    get OK /a { request Req  response Res }
    @errors(NonExistentErr)
    get Bad /b { request Req  response Res }
}
`,
	})
	// Exercise the project-level error-ref check directly: the per-package
	// pass skips @errors in project mode (it defers cross-package
	// resolution), so checkProjectErrorRefs is the only validator. A real
	// multi-package AnalyzeProject run needs an on-disk design root, so we
	// hand-build the project here - the valid-qualified-ref converse is
	// covered by the ecommerce e2e fixture instead.
	r := &refResolver{proj: &Project{Packages: map[string]*Package{
		"design": {Name: "design", Errors: map[string]*ast.ErrorDecl{
			"RealNotFound": {Category: "NotFound", Name: "RealNotFound"},
		}},
	}}}
	r.checkProjectErrorRefs(files)
	refs := 0
	for _, d := range r.diags {
		if d.Code == CodeDecoratorRef {
			refs++
			if !strings.Contains(d.Msg, "NonExistentErr") {
				t.Errorf("unexpected @errors ref diag: %s", d.Msg)
			}
		}
	}
	if refs != 1 {
		t.Errorf("expected exactly 1 @errors ref diag (the typo); the valid @errors(RealNotFound) must resolve. got %d", refs)
	}
}

// Note: the converse - a VALID cross-package @errors(shared.X) must NOT
// be rejected - is covered by the ecommerce e2e fixture (it uses
// `@errors(shared.UnauthorizedErr)` and gens cleanly through
// checkProjectErrorRefs). parseFileMap can't simulate the real package
// directories that import resolution needs, so it isn't unit-tested here.
