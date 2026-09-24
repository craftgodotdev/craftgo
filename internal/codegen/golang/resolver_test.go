package golang

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

// The resolver finds local symbols by bare name and other packages' only by qualified name.
func TestProjectResolverLookupRouting(t *testing.T) {
	r := buildProjectResolver(newFixtureProject(), newFixtureConfig(), "design")

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
}

func TestProjectResolverNilProjectStillUsable(t *testing.T) {
	r := buildProjectResolver(nil, newFixtureConfig(), "design")
	if r == nil {
		t.Fatal("BuildProjectResolver must return a non-nil zero resolver so callers can use it without a guard")
	}
	if r.LookupType("anything") != nil {
		t.Error("empty resolver lookups must miss")
	}
}

