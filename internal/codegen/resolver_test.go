package codegen

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

func newFixtureProject() *semantic.Project {
	return &semantic.Project{
		Packages: map[string]*semantic.Package{
			"design": {
				Name:    "design",
				Types:   map[string]*ast.TypeDecl{"Order": {Name: "Order"}},
				Enums:   map[string]*ast.EnumDecl{"Status": {Name: "Status"}},
				Scalars: map[string]*ast.ScalarDecl{"OrderID": {Name: "OrderID"}},
				Errors:  map[string]*ast.ErrorDecl{"OrderNotFound": {Name: "OrderNotFound"}},
				Middlewares: map[string]*ast.MiddlewareDecl{
					"Local": {Name: "Local"},
				},
			},
			"shared": {
				Name:    "shared",
				Types:   map[string]*ast.TypeDecl{"Page": {Name: "Page", TypeParams: []string{"T"}}},
				Enums:   map[string]*ast.EnumDecl{"Severity": {Name: "Severity"}},
				Scalars: map[string]*ast.ScalarDecl{"Email": {Name: "Email"}},
				Errors:  map[string]*ast.ErrorDecl{"NotFound": {Name: "NotFound"}},
				Middlewares: map[string]*ast.MiddlewareDecl{
					"AuthRequired": {Name: "AuthRequired"},
				},
			},
		},
	}
}

func newFixtureConfig() *config.Config {
	return &config.Config{
		Package: "github.com/test/m",
		Output:  config.Output{Types: "./internal/types"},
	}
}

// TestProjectResolverLookupRouting pins the keying contract: local
// symbols resolve under bare names, cross-package symbols only
// under their qualified DSL form. Without this contract the
// resolver collapses back into the local-only anti-pattern.
func TestProjectResolverLookupRouting(t *testing.T) {
	r := BuildProjectResolver(newFixtureProject(), newFixtureConfig(), "design")

	if r.LookupType("Order") == nil {
		t.Error("local type Order must resolve bare")
	}
	if r.LookupType("Page") != nil {
		t.Error("cross-pkg type must NOT leak under bare name (would shadow local lookups)")
	}
	if r.LookupType("shared.Page") == nil {
		t.Error("cross-pkg type must resolve under qualified form")
	}

	if r.LookupEnum("Status") == nil {
		t.Error("local enum Status must resolve bare")
	}
	if r.LookupEnum("shared.Severity") == nil {
		t.Error("cross-pkg enum must resolve under qualified form")
	}

	if r.LookupScalar("OrderID") == nil {
		t.Error("local scalar OrderID must resolve bare")
	}
	if r.LookupScalar("shared.Email") == nil {
		t.Error("cross-pkg scalar must resolve under qualified form")
	}

	if r.LookupError("OrderNotFound") == nil {
		t.Error("local error must resolve bare")
	}
	if r.LookupError("shared.NotFound") == nil {
		t.Error("cross-pkg error must resolve under qualified form")
	}

	if r.LookupMiddleware("Local") == nil {
		t.Error("local middleware must resolve bare")
	}
	if r.LookupMiddleware("shared.AuthRequired") == nil {
		t.Error("cross-pkg middleware must resolve under qualified form")
	}
}

func TestProjectResolverNilTolerant(t *testing.T) {
	var r *ProjectResolver
	if r.LookupType("anything") != nil ||
		r.LookupEnum("x") != nil ||
		r.LookupScalar("y") != nil ||
		r.LookupError("z") != nil ||
		r.LookupMiddleware("m") != nil {
		t.Error("nil receiver Lookup* must always return nil — emit sites use the result without a guard")
	}
	if r.ImportPath("shared") != "" {
		t.Error("nil receiver ImportPath must return empty string")
	}
	if prefix, path := r.QualifierFor(&ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"shared", "X"}}}); prefix != "" || path != "" {
		t.Error("nil receiver QualifierFor must return empty pair")
	}
}

func TestProjectResolverQualifierFor(t *testing.T) {
	r := BuildProjectResolver(newFixtureProject(), newFixtureConfig(), "design")

	// Cross-pkg ref: prefix + import path.
	q, path := r.QualifierFor(&ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"shared", "Page"}}})
	if q != "shared." {
		t.Errorf("qualifier = %q, want %q", q, "shared.")
	}
	if path != "github.com/test/m/internal/types/shared" {
		t.Errorf("import path = %q, want %q", path, "github.com/test/m/internal/types/shared")
	}

	// Bare ref: empty pair so the caller emits as-is without an
	// import registration.
	q, path = r.QualifierFor(&ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"Order"}}})
	if q != "" || path != "" {
		t.Errorf("bare ref must yield empty pair; got (%q, %q)", q, path)
	}

	// Unknown package alias: prefix kept (still a qualified ref),
	// import path empty (no CrossPkg entry) — caller decides whether
	// emit is safe.
	q, path = r.QualifierFor(&ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"unknown", "X"}}})
	if q != "unknown." {
		t.Errorf("unknown-alias qualifier must still carry the prefix; got %q", q)
	}
	if path != "" {
		t.Errorf("unknown-alias import path must be empty; got %q", path)
	}
}

func TestProjectResolverNilProjectStillUsable(t *testing.T) {
	r := BuildProjectResolver(nil, newFixtureConfig(), "design")
	if r == nil {
		t.Fatal("BuildProjectResolver must return a non-nil zero resolver so callers can use it without a guard")
	}
	if r.LookupType("anything") != nil {
		t.Error("empty resolver lookups must miss")
	}
}

func TestBuildErrorTableKeysQualifiedAcrossPackages(t *testing.T) {
	tbl := BuildErrorTable(newFixtureProject(), "design")
	if _, ok := tbl["OrderNotFound"]; !ok {
		t.Errorf("local error must appear bare: %v", tbl)
	}
	if _, ok := tbl["shared.NotFound"]; !ok {
		t.Errorf("cross-pkg error must appear under qualified form: %v", tbl)
	}
	if _, ok := tbl["NotFound"]; ok {
		t.Errorf("cross-pkg error must NOT leak under bare name: %v", tbl)
	}
}

func TestBuildMiddlewareTableKeysQualifiedAcrossPackages(t *testing.T) {
	tbl := BuildMiddlewareTable(newFixtureProject(), "design")
	if _, ok := tbl["Local"]; !ok {
		t.Errorf("local middleware must appear bare: %v", tbl)
	}
	if _, ok := tbl["shared.AuthRequired"]; !ok {
		t.Errorf("cross-pkg middleware must appear under qualified form: %v", tbl)
	}
}
