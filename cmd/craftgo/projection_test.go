package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/claim"
)

// claimsIn reads the claim records filed against one output directory.
func claimsIn(t *testing.T, root string) map[string]claim.Record {
	t.Helper()
	return claim.Read(root)
}

// claimed reports whether some design claims file under root.
func claimed(t *testing.T, root, file string) bool {
	t.Helper()
	for _, rec := range claimsIn(t, root) {
		for _, f := range rec.Files {
			if f == file {
				return true
			}
		}
	}
	return false
}

// rel renders path relative to root for a test message.
func rel(root, path string) string {
	out, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(out)
}

// shopDesign is a design whose services split across deployables: one
// publishes, two consume what it publishes.
const shopDesign = `package shop

type Order {
	id    string @minLength(1)
	total int32  @gte(0)
}

service Orders {
	event Placed { payload Order }
}

service Billing {
	consume ChargeOrder { event Placed }
}

service Shipping {
	consume ShipOrder { event Placed }
}
`

// monorepo lays out one design source and returns the repo root. The
// contract set lives in contracts/, every deployable under services/,
// and one go.mod at the top covers them all.
func monorepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, dir, "go.mod", "module github.com/test/mono\n\ngo 1.24\n")
	mustWrite(t, dir, "contracts/design/craftgo.design.yaml", "output:\n  kind: contracts\n")
	mustWrite(t, dir, "contracts/design/shop.craftgo", shopDesign)
	return dir
}

// projection writes a deployable that generates from the shared design
// and returns its project root.
func projection(t *testing.T, repo, name string, services ...string) string {
	t.Helper()
	root := filepath.Join(repo, "services", name)
	mustWrite(t, root, "design/craftgo.design.yaml", `design:
  from: ../../../contracts/design
  root: ../../../contracts
output:
  services: [`+strings.Join(services, ", ")+`]
`)
	return root
}

func genProjection(t *testing.T, root string) {
	t.Helper()
	if err := runGen([]string{"-f", filepath.Join(root, "design"), "-c", root}); err != nil {
		t.Fatalf("runGen %s: %v", root, err)
	}
}

func exists(t *testing.T, parts ...string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(parts...))
	return err == nil
}

// A deployable generates its application half from a design it does not
// contain, for the services it names and no others - the shape a
// consumer deployable needs, which until now meant copying the design.
func TestProjectionGeneratesTheServicesItNames(t *testing.T) {
	repo := monorepo(t)
	consumer := projection(t, repo, "consumer", "shop.Billing")
	genProjection(t, consumer)

	// The application half: only Billing's handler set and logic stub.
	if !exists(t, consumer, "internal", "transport", "billing_consumers.go") {
		t.Error("the named service's handler set must be generated")
	}
	if !exists(t, consumer, "internal", "service", "billing", "charge_order.go") {
		t.Error("the named service's logic stub must be generated")
	}
	if exists(t, consumer, "internal", "transport", "shipping_consumers.go") {
		t.Error("a service this deployable does not name must not reach its transport")
	}
	if exists(t, consumer, "internal", "service", "shipping") {
		t.Error("a service this deployable does not name must not get a logic stub")
	}

	// The contract half lands under the design source's own root, whole:
	// every service's, because the deployables share it.
	for _, rel := range []string{
		"gen/types/shop/types.go",
		"gen/events/orders/publisher.go",
		"gen/events/billing/consumers.go",
		"gen/events/shipping/consumers.go",
	} {
		if !exists(t, repo, "contracts", filepath.FromSlash(rel)) {
			t.Errorf("the contract half must be whole: %s is missing", rel)
		}
	}
	if exists(t, consumer, "gen", "types") {
		t.Error("a projection must not write a second copy of the contract half")
	}

	// The documents describe the design rather than this deployable's
	// slice of it - `output.services` selects what is generated.
	doc := read(t, filepath.Join(consumer, "docs", "asyncapi.yaml"))
	for _, op := range []string{"Billing consumes Placed", "Shipping consumes Placed", "Orders publishes Placed"} {
		if !strings.Contains(doc, op) {
			t.Errorf("the event map is the design's, and misses %q:\n%s", op, doc)
		}
	}

	// It is imported from the module path the design source's root
	// carries, not this deployable's.
	handler := read(t, filepath.Join(consumer, "internal", "transport", "billing_consumers.go"))
	if !strings.Contains(handler, `"github.com/test/mono/contracts/gen/types/shop"`) {
		t.Errorf("the handler must import the shared types:\n%s", handler)
	}
	stub := read(t, filepath.Join(consumer, "internal", "service", "billing", "charge_order.go"))
	if !strings.Contains(stub, `"github.com/test/mono/contracts/gen/types/shop"`) {
		t.Errorf("the logic stub must import the shared types:\n%s", stub)
	}
}

// Two deployables of one design write one contract half. They may do so
// because the input and the generator are the same, which is what the
// claim on the output is keyed to - the design source, not the manifest.
func TestTwoProjectionsOfOneDesignShareOneLibrary(t *testing.T) {
	repo := monorepo(t)
	library := filepath.Join(repo, "contracts")

	genProjection(t, projection(t, repo, "consumer", "shop.Billing"))
	first := treeOf(t, library)

	genProjection(t, projection(t, repo, "cronjob", "shop.Shipping"))
	second := treeOf(t, library)

	if len(first) == 0 {
		t.Fatal("the first projection wrote no contract half")
	}
	for path, body := range first {
		got, ok := second[path]
		if !ok {
			t.Errorf("the second projection removed %s", path)
			continue
		}
		if got != body {
			t.Errorf("the second projection rewrote %s - two projections of one design must agree", path)
		}
	}
	for path := range second {
		if _, ok := first[path]; !ok {
			t.Errorf("the second projection added %s to the shared half", path)
		}
	}

	// One claim covers the shared half, naming both manifests: they read
	// one design, so neither refuses the other.
	records := claimsIn(t, filepath.Join(library, "gen", "events"))
	if len(records) != 1 {
		t.Fatalf("two projections of one design must file one claim, got %d", len(records))
	}
	for _, rec := range records {
		if len(rec.Manifests) != 2 {
			t.Errorf("the claim must name both manifests, got %v", rec.Manifests)
		}
	}

	// Each keeps its own application half.
	if !exists(t, repo, "services", "consumer", "internal", "service", "billing", "charge_order.go") {
		t.Error("the first deployable's logic stub must survive")
	}
	if !exists(t, repo, "services", "cronjob", "internal", "service", "shipping", "ship_order.go") {
		t.Error("the second deployable must get its own logic stub")
	}
}

// The design's own project and a projection of it write one contract
// half too: same design, same generator, same bytes. Generating the
// contract set first is what a monorepo's `make gen` does.
func TestAProjectionAgreesWithTheDesignsOwnProject(t *testing.T) {
	repo := monorepo(t)
	library := filepath.Join(repo, "contracts")

	if err := runGen([]string{"-f", filepath.Join(library, "design"), "-c", library}); err != nil {
		t.Fatalf("runGen contracts: %v", err)
	}
	before := treeOf(t, library)
	genProjection(t, projection(t, repo, "consumer", "shop.Billing"))

	if got := treeOf(t, library); !sameTree(got, before) {
		t.Errorf("a projection rewrote the contract set's own output:\nbefore %v\nafter  %v", keysOf(before), keysOf(got))
	}
}

// Running one deployable again must leave the shared half exactly as it
// was: the prune reads the claim its design source filed, and the
// sibling writes the same files under it.
func TestRegeneratingAProjectionLeavesTheSharedHalfAlone(t *testing.T) {
	repo := monorepo(t)
	library := filepath.Join(repo, "contracts")
	consumer := projection(t, repo, "consumer", "shop.Billing")
	cronjob := projection(t, repo, "cronjob", "shop.Shipping")

	genProjection(t, consumer)
	genProjection(t, cronjob)
	before := treeOf(t, library)
	genProjection(t, consumer)

	if got := treeOf(t, library); !sameTree(got, before) {
		t.Errorf("a re-run changed the shared half:\nbefore %v\nafter  %v", keysOf(before), keysOf(got))
	}
	if !exists(t, repo, "services", "cronjob", "internal", "service", "shipping", "ship_order.go") {
		t.Error("the sibling deployable's output must survive")
	}
}

// The selection is checked against the analysed design before anything
// is written, and the error names what the entry nearly matched.
func TestProjectionRejectsAServiceTheDesignDoesNotDeclare(t *testing.T) {
	repo := monorepo(t)
	root := projection(t, repo, "consumer", "shop.Billng")

	err := runGen([]string{"-f", filepath.Join(root, "design"), "-c", root})
	if err == nil {
		t.Fatal("want a rejection, got none")
	}
	if !strings.Contains(err.Error(), "shop.Billng") || !strings.Contains(err.Error(), "shop.Billing") {
		t.Errorf("the error must name the entry and the near miss, got: %v", err)
	}
	if exists(t, root, "internal", "transport") {
		t.Error("nothing may be written before the selection is checked")
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// treeOf reads every generated file under root, keyed by its path
// relative to root, so two runs can be compared byte for byte. The claim
// records are left out: they are the ledger of who generates the
// directory, and it gains a line as each deployable joins.
func treeOf(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) == ".craftgo" || filepath.Base(path) == "craftgo.design.yaml" {
			return err
		}
		if strings.Contains(path, claim.Dir) {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		out[filepath.ToSlash(rel)] = string(body)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameTree(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for path, body := range a {
		if b[path] != body {
			return false
		}
	}
	return true
}

func keysOf(tree map[string]string) []string {
	out := make([]string, 0, len(tree))
	for path := range tree {
		out = append(out, path)
	}
	return out
}

// farmDesign is a second, unrelated design: different package, different
// contracts, its own consuming service.
const farmDesign = `package farm

type Animal {
	id string @minLength(1)
}

service Barn {
	event Fed { payload Animal }
}

service Tally {
	consume CountFed { event Fed }
}
`

// Two deployables of DIFFERENT designs pointed at one project root write
// one another's project-wide files - `transport/events.go`, the container,
// the documents - each a complete rewrite from its own design's point of
// view. The second run used to win silently, and which half of the
// consumers a binary subscribed depended on the order of the build.
func TestTwoDesignsCannotWriteOneDeployable(t *testing.T) {
	repo := monorepo(t)
	mustWrite(t, repo, "farm/design/craftgo.design.yaml", "output:\n  kind: contracts\n")
	mustWrite(t, repo, "farm/design/farm.craftgo", farmDesign)
	// Both deployables generate into the repo root: same transport, same
	// container, same documents.
	mustWrite(t, repo, "audit/design/craftgo.design.yaml", `design:
  from: ../../contracts/design
  root: ../../contracts
output:
  services: [shop.Billing]
`)
	mustWrite(t, repo, "tally/design/craftgo.design.yaml", `design:
  from: ../../farm/design
  root: ../../farm
output:
  services: [farm.Tally]
`)

	if err := runGen([]string{"-f", filepath.Join(repo, "audit", "design"), "-c", repo}); err != nil {
		t.Fatalf("the first deployable must generate: %v", err)
	}
	before := read(t, filepath.Join(repo, "internal", "transport", "events.go"))

	err := runGen([]string{"-f", filepath.Join(repo, "tally", "design"), "-c", repo})
	if err == nil {
		t.Fatal("a second design writing the first's files must be refused")
	}
	// Both designs and both manifests: the design is the identity the
	// rule turns on, the manifest is the file the user edits.
	for _, want := range []string{"contracts/design", "farm/design", "audit/design", "tally/design"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error must name %q, got: %v", want, err)
		}
	}

	// The project-wide files are the ones that clobbered: each is a
	// complete rewrite from one design's point of view, and each is now
	// claimed.
	for _, want := range []struct{ root, file string }{
		{filepath.Join(repo, "internal", "transport"), "events.go"},
		{filepath.Join(repo, "svccontext"), "events.go"},
		{filepath.Join(repo, "internal", "wiring"), "wiring.go"},
	} {
		if !claimed(t, want.root, want.file) {
			t.Errorf("%s/%s is regenerated and must be claimed", rel(repo, want.root), want.file)
		}
	}

	// Nothing of the first deployable's was touched.
	if got := read(t, filepath.Join(repo, "internal", "transport", "events.go")); got != before {
		t.Error("the refusal must come before anything is written")
	}
	if !strings.Contains(before, "Billing") {
		t.Errorf("the first deployable's subscriptions must survive:\n%s", before)
	}
	if exists(t, repo, "internal", "transport", "tally_consumers.go") {
		t.Error("the refused run must write none of its own output either")
	}
}
