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

service Orders {
	get ListOrders /orders { response Order }

	event Placed { payload Order }
}

service Billing {
	consume ChargeOrder { event Placed }
}

service Shipping {
	consume ShipOrder { event Placed }
}
`

// storeRecut is the same design with one consuming service renamed.
const storeRecut = `package store

type Order {
	id    string @minLength(1)
	total int32  @gte(0)
}

service Orders {
	get ListOrders /orders { response Order }

	event Placed { payload Order }
}

service Billing {
	consume ChargeOrder { event Placed }
}

service Dispatch {
	consume ShipOrder { event Placed }
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

// Renaming a service used to leave its handler set on disk: the run wrote
// the new one, pruned the library half, and left the application half
// importing a logic package it no longer generated - so the project no
// longer built and `craftgo gen` could not put it right.
func TestRenamedServiceLeavesNoApplicationHalfBehind(t *testing.T) {
	dir := storeProject(t, storeDesign)
	genProject(t, dir)

	for _, path := range []string{
		"internal/transport/shipping_consumers.go",
		"internal/events/shipping/consumers.go",
		"internal/service/shipping/ship_order.go",
	} {
		if !exists(t, dir, filepath.FromSlash(path)) {
			t.Fatalf("the first run must write %s", path)
		}
	}

	mustWrite(t, dir, "design/store.craftgo", storeRecut)
	genProject(t, dir)

	// Both halves of the service that is gone go, and so does the claim
	// entry that named them - the only record of what used to be here.
	if exists(t, dir, "internal", "transport", "shipping_consumers.go") {
		t.Error("the handler set of a renamed service must go - it imports a logic package the run no longer writes")
	}
	if exists(t, dir, "internal", "events", "shipping") {
		t.Error("the library package of a renamed service must go")
	}
	if claimed(t, filepath.Join(dir, "internal", "transport"), "shipping_consumers.go") {
		t.Error("the claim must stop naming the handler set")
	}
	if claimed(t, filepath.Join(dir, "internal", "events"), "shipping/consumers.go") {
		t.Error("the claim must stop naming the library package")
	}

	// The logic stub is gen-once - the user's own code - so it is never
	// claimed and never pruned.
	if !exists(t, dir, "internal", "service", "shipping", "ship_order.go") {
		t.Error("a gen-once logic stub must survive the service that seeded it")
	}

	for _, path := range []string{
		"internal/transport/dispatch_consumers.go",
		"internal/transport/billing_consumers.go",
		"internal/events/dispatch/consumers.go",
		"internal/service/dispatch/ship_order.go",
		"internal/routes/orders/routes.go",
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
