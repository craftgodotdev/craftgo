package main

import (
	"os"
	"path/filepath"
	"testing"
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

func exists(t *testing.T, parts ...string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(parts...))
	return err == nil
}

// treeOf reads every generated file under root, keyed by its path
// relative to root, so two runs can be compared byte for byte.
func treeOf(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) == ".craftgo" || filepath.Base(path) == "craftgo.design.yaml" {
			return err
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
// wrote the new one, swept nothing, and left a routes file pointing at a
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

	// Every regenerated file of the service that is gone goes, and the
	// directories the removals empty go with them.
	if exists(t, dir, "internal", "transport", "orders") {
		t.Error("the handlers of a renamed service must go - the routes file no longer registers them")
	}
	if exists(t, dir, "internal", "routes", "orders") {
		t.Error("the routes of a renamed service must go")
	}

	// The logic stub is gen-once - the user's own code - so its directory
	// is never swept.
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
}

// The sweep goes by the generated header, so a file craftgo did not write
// survives the run that clears the generated ones around it - and holds
// its directory open.
func TestHandWrittenFilesInAnOutputDirectorySurvive(t *testing.T) {
	dir := storeProject(t, storeDesign)
	genProject(t, dir)

	mustWrite(t, dir, "internal/transport/orders/helper.go", "package orders\n\nfunc Helper() {}\n")
	mustWrite(t, dir, "internal/events/store/notes.go", "package store\n\n// mine\n")

	mustWrite(t, dir, "design/store.craftgo", storeRecut)
	genProject(t, dir)

	for _, path := range []string{
		"internal/transport/orders/helper.go",
		"internal/events/store/notes.go",
	} {
		if !exists(t, dir, filepath.FromSlash(path)) {
			t.Errorf("%s is hand-written and must survive", path)
		}
	}
	if exists(t, dir, "internal", "transport", "orders", "list_orders.go") {
		t.Error("the handler of the service that is gone must still go")
	}
}

// A design that drops its last service loses the whole HTTP half - the
// per-service routes, the umbrella that registers them, the handlers.
// Leaving the umbrella behind means a generated file calling into
// packages the run no longer emits, and the project stops building.
func TestDroppingTheLastServiceClearsTheHTTPHalf(t *testing.T) {
	dir := storeProject(t, storeDesign)
	genProject(t, dir)
	if !exists(t, dir, "internal", "routes", "routes.go") {
		t.Fatal("the first run must write the routes umbrella")
	}

	mustWrite(t, dir, "design/store.craftgo", `package store

type Order {
	id    string @minLength(1)
	total int32  @gte(0)
}

event Placed { payload Order }
`)
	genProject(t, dir)

	// The output roots stay - a run that leaves one bare still owns it -
	// but nothing generated is left inside them.
	for _, path := range []string{"internal/routes/routes.go", "internal/routes/orders", "internal/transport/orders"} {
		if exists(t, dir, filepath.FromSlash(path)) {
			t.Errorf("%s must go with the last service", path)
		}
	}
	if !exists(t, dir, "internal", "events", "store", "events.go") {
		t.Error("the contracts the design still declares must survive")
	}
}
