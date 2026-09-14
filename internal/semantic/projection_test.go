package semantic

import (
	"strings"
	"testing"
)

// shopFixture is a design whose services split cleanly across
// deployables: one publishes, two consume what it publishes.
func shopFixture(t *testing.T) *Project {
	t.Helper()
	root, files := projectFixture(t, map[string]string{
		"shop/shop.craftgo": `package shop
type Order { id string @minLength(1) }
service Orders {
	event Placed { payload Order }
}
service Billing {
	consume ChargeOrder { event Placed }
}
service Shipping {
	consume ShipOrder { event Placed }
}`,
	})
	proj, diags := AnalyzeProject(files, Options{DesignRoot: root})
	for _, d := range diags {
		if d.IsError() {
			t.Fatalf("fixture must analyse cleanly: %s", d.Msg)
		}
	}
	return proj
}

// A projection narrows the services - what a deployable runs - and
// nothing else: the types and the event contracts are the design's
// shared vocabulary, and a service it runs may consume a contract
// another service publishes.
func TestProjectionNarrowsServicesAndKeepsTheVocabulary(t *testing.T) {
	proj := shopFixture(t)
	billing, err := proj.Projection([]string{"shop.Billing"})
	if err != nil {
		t.Fatal(err)
	}
	pkg := billing.Packages["shop"]
	if len(pkg.Services) != 1 || pkg.Services["Billing"] == nil {
		t.Errorf("projected services = %v, want only Billing", serviceNames(pkg))
	}
	if len(pkg.Consumers) != 1 || pkg.Consumers["Billing.ChargeOrder"] == nil {
		t.Errorf("a consumer belongs to its service, got %d", len(pkg.Consumers))
	}
	if pkg.Types["Order"] == nil {
		t.Error("the payload type is shared vocabulary and must survive")
	}
	if _, ok := billing.LookupEvent("shop", "Placed"); !ok {
		t.Error("a consumer must still resolve a contract another service publishes")
	}
	// The whole design stays reachable: the contract half is generated
	// from it whatever this deployable runs.
	if got := serviceNames(billing.Design().Packages["shop"]); len(got) != 3 {
		t.Errorf("Design() = %v, want every service", got)
	}
	if proj.Design() != proj {
		t.Error("a design that projects everything is its own design")
	}
}

// An empty selection is every service - what a project generating the
// whole design asks for, and what every manifest without the key gets.
func TestEmptyProjectionIsTheWholeDesign(t *testing.T) {
	proj := shopFixture(t)
	all, err := proj.Projection(nil)
	if err != nil {
		t.Fatal(err)
	}
	if all != proj {
		t.Error("an empty selection must hand back the design itself")
	}
}

// A manifest naming a service the design does not declare is a typo or a
// rename, and either way generates nothing. The near misses are what
// turns the rejection into a fix.
func TestUnknownServiceNamesTheNearMisses(t *testing.T) {
	proj := shopFixture(t)
	_, err := proj.Projection([]string{"shop.Billng"})
	if err == nil {
		t.Fatal("want a rejection, got none")
	}
	if !strings.Contains(err.Error(), "shop.Billng") || !strings.Contains(err.Error(), "shop.Billing") {
		t.Errorf("the error must name the entry and what it nearly matches, got: %v", err)
	}

	// A package nobody declares has no siblings to suggest, so the
	// design's own services are listed instead.
	_, err = proj.Projection([]string{"warehouse.Billing"})
	if err == nil || !strings.Contains(err.Error(), "shop.Billing") {
		t.Errorf("want the declared services listed, got: %v", err)
	}
}

func serviceNames(pkg *Package) []string {
	var out []string
	for name := range pkg.Services {
		out = append(out, name)
	}
	return out
}
