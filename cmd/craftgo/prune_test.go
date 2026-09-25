package main

import (
	"os"
	"path/filepath"
	"testing"
)

// storeDesign is a package with a type, an event and one HTTP service.
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

// auditDesign is a second package with no service, so all its output lands
// under `output.types`.
const auditDesign = `package audit

enum Level { Low = 1  High = 2 }

type Entry {
	id    string @minLength(1)
	level Level
}
`

// storeProject lays out a project whose design is src and returns its root.
func storeProject(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, dir, "go.mod", "module github.com/test/store\n\ngo 1.24\n")
	mustWrite(t, dir, "design/craftgo.design.yaml", "")
	mustWrite(t, dir, "design/store.craftgo", src)
	return dir
}

// TestRenamedServiceLeavesNoApplicationHalfBehind checks that renaming a
// service sweeps its old handlers and routes, keeps its logic stub, and that
// a further run changes nothing.
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

	// The old service's generated files go, with the directories they empty.
	if exists(t, dir, "internal", "transport", "orders") {
		t.Error("the handlers of a renamed service must go - the routes file no longer registers them")
	}
	if exists(t, dir, "internal", "routes", "orders") {
		t.Error("the routes of a renamed service must go")
	}

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

// TestRemovedPackageLosesItsTypesFolder checks that removing a DSL package
// removes its types folder.
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

// TestPackageThatStopsPublishingLosesItsEventLibrary checks that a package
// left with no event loses its event library folder.
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

// TestHandWrittenFilesInAnOutputDirectorySurvive checks that the sweep, which
// goes by the generated header, keeps hand-written files and their directory.
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

// TestDroppingTheLastServiceClearsTheHTTPHalf checks that dropping the last
// service removes the routes umbrella, the service routes and the handlers.
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

	for _, path := range []string{"internal/routes/routes.go", "internal/routes/orders", "internal/transport/orders"} {
		if exists(t, dir, filepath.FromSlash(path)) {
			t.Errorf("%s must go with the last service", path)
		}
	}
	if !exists(t, dir, "internal", "events", "store", "events.go") {
		t.Error("the contracts the design still declares must survive")
	}
}
