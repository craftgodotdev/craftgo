package golang

import (
	"strings"
	"testing"
)

// main.go sets the body-size default with SetDefaultMaxBodySize, which @maxBodySize overrides.
func TestGenerateMainUsesSetDefaultMaxBodySize(t *testing.T) {
	data := mainData{
		HasRoutes:        true,
		ConfigImport:     "example.com/app/config",
		WiringImport:     "example.com/app/internal/wiring",
		SvccontextImport: "example.com/app/svccontext",
	}
	out, err := renderScaffold(tmpl("main.tmpl"), data)
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

// main.go sets the handler timeout with SetDefaultHandlerTimeout, which @timeout overrides.
func TestGenerateMainUsesSetDefaultHandlerTimeout(t *testing.T) {
	data := mainData{
		HasRoutes:        true,
		ConfigImport:     "example.com/app/config",
		WiringImport:     "example.com/app/internal/wiring",
		SvccontextImport: "example.com/app/svccontext",
	}
	out, err := renderScaffold(tmpl("main.tmpl"), data)
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

// main.go installs telemetry before AccessLog and both guard defaults before wiring.Register.
func TestGenerateMainOrdersTheWiring(t *testing.T) {
	data := mainData{
		HasRoutes:        true,
		ConfigImport:     "example.com/app/config",
		WiringImport:     "example.com/app/internal/wiring",
		SvccontextImport: "example.com/app/svccontext",
	}
	out, err := renderScaffold(tmpl("main.tmpl"), data)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, pair := range []struct{ rule, first, second string }{
		{"the span must be open before the access line is written",
			"server.WithTelemetry(tel.HTTPMiddleware())", "srv.Use(server.AccessLog("},
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
