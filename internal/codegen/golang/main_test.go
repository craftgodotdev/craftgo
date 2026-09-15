package golang

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
		WiringImport:     "example.com/app/internal/wiring",
		SvccontextImport: "example.com/app/svccontext",
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

// The scaffolded main.go installs the handler-timeout default via
// SetDefaultHandlerTimeout - which a per-method @timeout overrides - rather than
// a blanket srv.Use(server.Timeout(...)) middleware, which clamps every route to
// min(default, per-method).
func TestGenerateMainUsesSetDefaultHandlerTimeout(t *testing.T) {
	data := mainData{
		ConfigImport:     "example.com/app/config",
		WiringImport:     "example.com/app/internal/wiring",
		SvccontextImport: "example.com/app/svccontext",
	}
	out, err := renderGo(tmpl("main.tmpl"), data)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "srv.SetDefaultHandlerTimeout(cfg.Server.HandlerTimeout)") {
		t.Errorf("main.go should install the timeout default via SetDefaultHandlerTimeout:\n%s", got)
	}
	if strings.Contains(got, "srv.Use(server.Timeout(") {
		t.Errorf("main.go must not use a blanket Timeout middleware (not overridable):\n%s", got)
	}
}

// The scaffold emits the telemetry wrapper ahead of AccessLog, so the access
// line carries the span's ids, and both guard defaults ahead of
// wiring.Register, so every route resolves its timeout and body cap at
// registration.
func TestGenerateMainOrdersTheWiring(t *testing.T) {
	data := mainData{
		ConfigImport:     "example.com/app/config",
		WiringImport:     "example.com/app/internal/wiring",
		SvccontextImport: "example.com/app/svccontext",
	}
	out, err := renderGo(tmpl("main.tmpl"), data)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, pair := range []struct{ rule, first, second string }{
		{"the span must be open before the access line is written",
			"srv.Use(tel.HTTPMiddleware())", "srv.Use(server.AccessLog("},
		{"the timeout default must be set before routes resolve it",
			"srv.SetDefaultHandlerTimeout(", "wiring.Register("},
		{"the body cap must be set before routes resolve it",
			"srv.SetDefaultMaxBodySize(", "wiring.Register("},
	} {
		first, second := strings.Index(got, pair.first), strings.Index(got, pair.second)
		switch {
		case first < 0:
			t.Errorf("%s: main.go emits no %s:\n%s", pair.rule, pair.first, got)
		case second < 0:
			t.Errorf("%s: main.go emits no %s:\n%s", pair.rule, pair.second, got)
		case first > second:
			t.Errorf("%s: main.go emits %s at offset %d, after %s at offset %d",
				pair.rule, pair.first, first, pair.second, second)
		}
	}
}
