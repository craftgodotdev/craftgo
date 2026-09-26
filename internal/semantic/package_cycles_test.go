package semantic

import (
	"slices"
	"testing"
)

// cycleDiags analyses src (design-relative path → content) as one project and
// returns its diagnostics.
func cycleDiags(t *testing.T, src map[string]string) []Diagnostic {
	t.Helper()
	root, files := projectFixture(t, src)
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	return diags
}

// Two packages whose fields name each other's types form one cycle.
func TestPackageCycleThroughFields(t *testing.T) {
	diags := cycleDiags(t, map[string]string{
		"app/a.craftgo": `package app
type Audit { who string }
type Req { b shared.Base }`,
		"shared/s.craftgo": `package shared
type Base { a app.Audit }`,
	})
	if got := codes(diags); !slices.Equal(got, []string{CodeRefPackageCycle}) {
		t.Fatalf("want one %s, got %v", CodeRefPackageCycle, diags)
	}
	expectMessage(t, &diags[0], "app -> shared -> app")
	if len(diags[0].Related) != 1 {
		t.Errorf("want the closing reference related, got %+v", diags[0].Related)
	}
}

// Mixins, generic arguments and error bodies reference packages too.
func TestPackageCycleThroughMixinsArgsAndErrors(t *testing.T) {
	cases := map[string]map[string]string{
		"mixin": {
			"app/a.craftgo": `package app
type Audit { who string }
type Req { shared.Base  x string }`,
			"shared/s.craftgo": `package shared
type Base { app.Audit  b string }`,
		},
		"generic argument": {
			"app/a.craftgo": `package app
type Audit { who string }
type Box<T> { v T }
type Req { b Box<shared.Base> }`,
			"shared/s.craftgo": `package shared
type Base { a app.Audit }`,
		},
		"error body": {
			"app/a.craftgo": `package app
type Audit { who string }
error NotFound Gone { b shared.Base }`,
			"shared/s.craftgo": `package shared
type Base { a app.Audit }`,
		},
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			diags := cycleDiags(t, src)
			if got := codes(diags); !slices.Equal(got, []string{CodeRefPackageCycle}) {
				t.Fatalf("want one %s, got %v", CodeRefPackageCycle, diags)
			}
		})
	}
}

// A cycle through three packages is reported once, with its whole path.
func TestPackageCycleOfThreeReportedOnce(t *testing.T) {
	diags := cycleDiags(t, map[string]string{
		"a/a.craftgo": `package a
type A { b b.B }`,
		"b/b.craftgo": `package b
type B { c c.C }`,
		"c/c.craftgo": `package c
type C { a a.A? }`,
	})
	if got := codes(diags); !slices.Equal(got, []string{CodeRefPackageCycle}) {
		t.Fatalf("want one %s, got %v", CodeRefPackageCycle, diags)
	}
	expectMessage(t, &diags[0], "a -> b -> c -> a")
	if len(diags[0].Related) != 2 {
		t.Errorf("want the two later references related, got %+v", diags[0].Related)
	}
}

// Packages that reference each other one way only, and an event payload,
// which lives outside the types package, form no cycle.
func TestPackageReferencesWithoutCycle(t *testing.T) {
	diags := cycleDiags(t, map[string]string{
		"orders/o.craftgo": `package orders
type Order { c shared.Customer }
event Placed { payload Order }`,
		"shared/s.craftgo": `package shared
type Customer { id string }
event OrderSeen { payload orders.Order }`,
	})
	expectNoDiags(t, diags)
}
