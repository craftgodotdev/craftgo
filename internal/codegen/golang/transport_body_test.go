package golang

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bodySrc holds a body whose members the handler checks, one it decodes straight, a route-filled
// member and a type-parameter field over a list.
const bodySrc = `package design
type Item { sku string  qty int? }
type Box<T> { v T  opt T? }
type Make { name string  items Item[]  boxed Box<Item[]> }
type Loose { note string?  size int? @default(3) }
type Rename { id string  name string }
service S {
	post Make /make { request Make }
	post Loose /loose { request Loose }
	put Rename /things/{id} { request Rename }
}`

// generateBodies writes bodySrc's handlers and returns the segment's transport directory.
func generateBodies(t *testing.T) string {
	t.Helper()
	pkg := analyze(t, bodySrc)
	root := t.TempDir()
	if err := generateTransport(pkg, sampleConfig(), root, nil); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, "internal/transport/s")
}

// A body with a required member decodes into its wire struct while the check is on, else straight
// into the request; one with optional members only decodes straight, its defaults pre-filled.
func TestBodyDecodesThroughItsWireStruct(t *testing.T) {
	dir := generateBodies(t)
	mk := readGen(t, dir, "make.go")
	mustParseGo(t, mk)
	mustContainAll(t, mk,
		"if server.RequirePresence() {",
		"var _b makeWire",
		"if _m := _b.to(&req); _m != \"\" {",
		"server.WriteValidationError(w, r, server.MissingMember(_m))",
		"} else if err := server.JSON().Decode(r.Body, &req); err != nil {",
	)
	loose := readGen(t, dir, "loose.go")
	mustContainNone(t, loose, "RequirePresence", "Wire")
	mustContainAll(t, loose, "server.JSON().Decode(r.Body, &req)")
}

// The wire struct checks each required member at its JSON path, decodes a type-parameter `T?`
// over a list behind a pointer as the request type holds it, and leaves a member the route fills
// unchecked.
func TestBodyWireStructs(t *testing.T) {
	dir := generateBodies(t)
	src := readGen(t, dir, bodyFile)
	mustParseGo(t, src)
	mustContainAll(t, collapseSpace(src),
		"type makeWire struct {",
		"Items []itemWire `json:\"items\"`",
		"Boxed *boxItemListWire `json:\"boxed\"`",
		"Opt *[]itemWire `json:\"opt\"`",
		"func (w *boxItemListWire) to(dst *types.Box[[]types.Item]) string {",
		"dst.Opt = new([]types.Item)",
		"return \"items.\" + _p",
		"return \"name\"",
	)
	_, rename, ok := strings.Cut(src, "func (w *renameWire) to")
	if !ok {
		t.Fatalf("no renameWire:\n%s", src)
	}
	mustContainNone(t, rename, `return "id"`)
	mustContainAll(t, rename, `return "name"`)
}

// A segment whose bodies all decode straight writes no wire file.
func TestBodyFileOnlyWhenAWireStructIs(t *testing.T) {
	pkg := analyze(t, `package design
type Loose { note string? }
service S { post Loose /loose { request Loose } }`)
	root := t.TempDir()
	if err := generateTransport(pkg, sampleConfig(), root, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "internal/transport/s", bodyFile)); !os.IsNotExist(err) {
		t.Errorf("want no %s: %v", bodyFile, err)
	}
}

// Services a @group puts in one directory share one bodyFile, holding the wire structs of every
// one of their handlers, a type they share declared once.
func TestBodyFileOfServicesSharingADirectory(t *testing.T) {
	pkg := analyze(t, `package design
type Item { sku string }
type Order { items Item[] }
type Session { items Item[]  token string }
@group("checkout")
service Orders { post Create /orders { request Order } }
@group("checkout")
service Sessions { post Open /sessions { request Session } }`)
	root := t.TempDir()
	if err := generateTransport(pkg, sampleConfig(), root, nil); err != nil {
		t.Fatal(err)
	}
	src := readGen(t, filepath.Join(root, "internal/transport/checkout"), bodyFile)
	mustParseGo(t, src)
	mustContainAll(t, src, "type orderWire struct {", "type sessionWire struct {")
	if n := strings.Count(src, "type itemWire struct {"); n != 1 {
		t.Errorf("itemWire declared %d times:\n%s", n, src)
	}
}
