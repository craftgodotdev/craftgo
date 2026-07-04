package codegen

import (
	"strings"
	"testing"
)

// The scaffolded main.go installs the request-body default via
// SetDefaultMaxBodySize - which a per-method @maxBodySize overrides - rather
// than a blanket srv.Use(server.BodyLimit(...)) middleware, which wraps every
// route and could not be overridden by a larger per-method cap.
func TestGenerateMainUsesSetDefaultMaxBodySize(t *testing.T) {
	data := mainData{
		ConfigImport:     "example.com/app/config",
		RoutesImport:     "example.com/app/internal/routes",
		SvccontextImport: "example.com/app/svccontext",
		OperationName:    "app",
	}
	out, err := renderGo(tmpl("main.tmpl"), data)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "srv.SetDefaultMaxBodySize(cfg.Server.MaxBodySize)") {
		t.Errorf("main.go should install the body default via SetDefaultMaxBodySize:\n%s", got)
	}
	if strings.Contains(got, "srv.Use(server.BodyLimit") {
		t.Errorf("main.go must not use a blanket BodyLimit middleware (not overridable):\n%s", got)
	}
}
