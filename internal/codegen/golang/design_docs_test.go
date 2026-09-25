package golang

import (
	"os"
	"path/filepath"
	"testing"
)

// Every Go declaration the design names carries the design's description, its @doc else its
// comment, and an empty `//` line parts it from the lines the generator adds.
func TestGoDocsCarryTheDesignDescription(t *testing.T) {
	proj := analyzeProject(t, `package app
// Cents is an amount in cents.
scalar Cents int @gte(0)
// Status says where an order is.
enum Status {
	// Open orders take changes.
	Open
	Closed
}
// Missing is returned for an unknown order.
error NotFound Missing
// This comment gives way to the @doc.
@doc("An order line.")
type Line {
	qty    int    @doc("How many.")
	status Status?
	price  Cents?
}
// Audit records who called.
middleware Audit
service Orders {
	// This comment gives way to the @doc.
	@doc("Fetch a line.")
	@middlewares(Audit)
	post Get /get { request Line  response Line }
}`)
	cfg := sampleConfig()
	cfg.Output.Middleware = "./internal/middleware"
	root := t.TempDir()
	if err := Generate(proj, nil, cfg, root); err != nil {
		t.Fatal(err)
	}
	read := func(rel string) string {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	mustContainAll(t, read("internal/types/app/types.go"),
		"// Cents is an amount in cents.\ntype Cents int",
		"// An order line.\ntype Line struct {",
		"\t// How many.\n\tQty ",
	)
	mustContainAll(t, read("internal/types/app/enums.go"),
		"// Status says where an order is.\ntype Status string",
		"\t// Open orders take changes.\n\tStatusOpen ",
	)
	mustContainAll(t, read("internal/types/app/errors.go"),
		"// Missing is returned for an unknown order.\n//\n// MissingErr is the NotFound error Missing.",
	)
	mustContainAll(t, read("internal/transport/orders/get.go"), "// Fetch a line.\n//\n// Get returns the POST Get handler.")
	mustContainAll(t, read("internal/service/orders/get.go"), "// Fetch a line.\n//\n// Get implements Orders.Get.")
	mustContainAll(t, read("svccontext/middlewares.go"), "// Audit records who called.\n//\n// AuditMiddleware is the Audit middleware.")
	mustContainAll(t, read("internal/middleware/audit-middleware.go"), "// Audit records who called.\n//\n// NewAuditMiddleware returns the Audit middleware")
	for _, rel := range []string{"internal/types/app/types.go", "internal/transport/orders/get.go", "internal/service/orders/get.go"} {
		mustContainNone(t, read(rel), "This comment gives way")
	}
}
