package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/claim"
)

// storeDesign is the shape a recut exercises: an HTTP service that also
// publishes, and two services consuming what it publishes.
const storeDesign = `package store

type Order {
	id    string @minLength(1)
	total int32  @gte(0)
}

event Placed { payload Order }

service Orders {
	get ListOrders /orders { response Order }
}
`

// storeRecut is the same design with the HTTP service renamed.
const storeRecut = `package store

type Order {
	id    string @minLength(1)
	total int32  @gte(0)
}

event Placed { payload Order }

service Catalog {
	get ListOrders /orders { response Order }
}
`

// auditDesign is a second package, contracts only: no service, so
// everything it produces lands under `output.types`.
const auditDesign = `package audit

enum Level { Low = 1  High = 2 }

type Entry {
	id    string @minLength(1)
	level Level
}
`

// claimed reports whether some design claims file under root.
func claimed(t *testing.T, root, file string) bool {
	t.Helper()
	for _, rec := range claim.Read(root) {
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

func exists(t *testing.T, parts ...string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(parts...))
	return err == nil
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

// storeProject lays out a project holding its own design and returns its
// root.
func storeProject(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, dir, "go.mod", "module github.com/test/store\n\ngo 1.24\n")
	mustWrite(t, dir, "design/craftgo.design.yaml", "")
	mustWrite(t, dir, "design/store.craftgo", src)
	return dir
}

func genProject(t *testing.T, dir string) {
	t.Helper()
	if err := runGen([]string{"-f", filepath.Join(dir, "design"), "-c", dir}); err != nil {
		t.Fatalf("runGen %s: %v", dir, err)
	}
}

// Renaming a service used to leave its application half on disk: the run
// wrote the new one, pruned nothing, and left a routes file pointing at a
// transport package it no longer generated - so the project no longer
// built and `craftgo gen` could not put it right.
func TestRenamedServiceLeavesNoApplicationHalfBehind(t *testing.T) {
	dir := storeProject(t, storeDesign)
	genProject(t, dir)

	for _, path := range []string{
		"internal/transport/orders/list_orders.go",
		"internal/routes/orders/routes.go",
		"internal/service/orders/list_orders.go",
		"internal/events/store/events.go",
	} {
		if !exists(t, dir, filepath.FromSlash(path)) {
			t.Fatalf("the first run must write %s", path)
		}
	}

	mustWrite(t, dir, "design/store.craftgo", storeRecut)
	genProject(t, dir)

	// Every regenerated file of the service that is gone goes, and so
	// does the claim entry that named them - the only record of what used
	// to be here.
	if exists(t, dir, "internal", "transport", "orders") {
		t.Error("the handlers of a renamed service must go - the routes file no longer registers them")
	}
	if exists(t, dir, "internal", "routes", "orders") {
		t.Error("the routes of a renamed service must go")
	}
	if claimed(t, filepath.Join(dir, "internal", "transport"), "orders/list_orders.go") {
		t.Error("the claim must stop naming the handler")
	}

	// The logic stub is gen-once - the user's own code - so it is never
	// claimed and never pruned.
	if !exists(t, dir, "internal", "service", "orders", "list_orders.go") {
		t.Error("a gen-once logic stub must survive the service that seeded it")
	}

	for _, path := range []string{
		"internal/transport/catalog/list_orders.go",
		"internal/routes/catalog/routes.go",
		"internal/events/store/events.go",
	} {
		if !exists(t, dir, filepath.FromSlash(path)) {
			t.Errorf("the recut design must still produce %s", path)
		}
	}

	before := treeOf(t, dir)
	genProject(t, dir)
	if got := treeOf(t, dir); !sameTree(got, before) {
		t.Errorf("a third run must change nothing:\nbefore %v\nafter  %v", keysOf(before), keysOf(got))
	}
}

// A whole DSL package can go the same way a service can, and its types
// folder goes with it: every file in it carries the generated header and
// nothing the design declares imports them any more.
func TestRemovedPackageLosesItsTypesFolder(t *testing.T) {
	dir := storeProject(t, storeDesign)
	mustWrite(t, dir, "design/audit.craftgo", auditDesign)
	genProject(t, dir)

	for _, name := range []string{"types.go", "validate.go", "enums.go"} {
		if !exists(t, dir, "internal", "types", "audit", name) {
			t.Fatalf("the first run must write internal/types/audit/%s", name)
		}
	}

	if err := os.Remove(filepath.Join(dir, "design", "audit.craftgo")); err != nil {
		t.Fatal(err)
	}
	genProject(t, dir)

	if exists(t, dir, "internal", "types", "audit") {
		t.Error("a package the design no longer declares must lose its types folder, directory included")
	}
	if claimed(t, filepath.Join(dir, "internal", "types"), "audit/types.go") {
		t.Error("the claim must stop naming the removed package's files")
	}
	if !exists(t, dir, "internal", "types", "store", "types.go") {
		t.Error("the package that survives keeps its types")
	}
}

// A design that stops declaring events loses its event library, folder
// included: nothing else in the tree may import a descriptor the run no
// longer writes.
func TestPackageThatStopsPublishingLosesItsEventLibrary(t *testing.T) {
	dir := storeProject(t, storeDesign)
	genProject(t, dir)
	if !exists(t, dir, "internal", "events", "store", "events.go") {
		t.Fatal("the first run must write the event library")
	}

	mustWrite(t, dir, "design/store.craftgo", `package store

type Order {
	id    string @minLength(1)
	total int32  @gte(0)
}

service Orders {
	get ListOrders /orders { response Order }
}
`)
	genProject(t, dir)

	if exists(t, dir, "internal", "events", "store") {
		t.Error("a design with no event must lose its event library, directory included")
	}
	if claimed(t, filepath.Join(dir, "internal", "events"), "store/events.go") {
		t.Error("the claim must stop naming the library")
	}
}

// farmDesign is a second, unrelated design: different package, different
// contracts.
const farmDesign = `package farm

type Animal {
	id string @minLength(1)
}

event Fed { payload Animal }
`

// Two designs pointed at one project root write one another's
// project-wide files - the wiring package, the container, the documents -
// each a complete rewrite from its own design's point of view. The second
// run used to win silently, and which half of the project a binary
// carried depended on the order of the build.
func TestTwoDesignsCannotWriteOneProject(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, repo, "go.mod", "module github.com/test/repo\n\ngo 1.24\n")
	mustWrite(t, repo, "store/design/craftgo.design.yaml", "")
	mustWrite(t, repo, "store/design/store.craftgo", storeDesign)
	mustWrite(t, repo, "farm/design/craftgo.design.yaml", "")
	mustWrite(t, repo, "farm/design/farm.craftgo", farmDesign)

	if err := runGen([]string{"-f", filepath.Join(repo, "store", "design"), "-c", repo}); err != nil {
		t.Fatalf("the first design must generate: %v", err)
	}
	before := read(t, filepath.Join(repo, "internal", "wiring", "wiring.go"))

	err := runGen([]string{"-f", filepath.Join(repo, "farm", "design"), "-c", repo})
	if err == nil {
		t.Fatal("a second design writing the first's files must be refused")
	}
	// Both designs and both manifests: the design is the identity the
	// rule turns on, the manifest is the file the user edits.
	for _, want := range []string{"store/design", "farm/design"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error must name %q, got: %v", want, err)
		}
	}

	for _, want := range []struct{ root, file string }{
		{filepath.Join(repo, "internal", "wiring"), "wiring.go"},
		{filepath.Join(repo, "svccontext"), "middlewares.go"},
	} {
		if !claimed(t, want.root, want.file) {
			t.Errorf("%s/%s is regenerated and must be claimed", rel(repo, want.root), want.file)
		}
	}

	// Nothing of the first design's was touched.
	if got := read(t, filepath.Join(repo, "internal", "wiring", "wiring.go")); got != before {
		t.Error("the refusal must come before anything is written")
	}
	if exists(t, repo, "internal", "events", "farm") {
		t.Error("the refused run must write none of its own output either")
	}
}
