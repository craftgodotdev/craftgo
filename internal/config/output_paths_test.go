package config

import (
	"reflect"
	"strings"
	"testing"
)

// notAPath names the Output string fields that are not paths; the test treats
// every other string field as one.
var notAPath = map[string]bool{"Kind": true, "FileCase": true}

func TestEveryOutputPathStaysInsideTheProject(t *testing.T) {
	typ := reflect.TypeOf(Output{})
	checked := 0
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Type.Kind() != reflect.String || notAPath[f.Name] {
			continue
		}
		checked++
		t.Run(f.Name, func(t *testing.T) {
			cfg := &Config{}
			reflect.ValueOf(&cfg.Output).Elem().Field(i).SetString("../outside")
			err := cfg.validate()
			if err == nil {
				t.Fatalf("output.%s accepted a path outside the project - gen writes there and the import fails later at `go build`", f.Name)
			}
			if !strings.Contains(err.Error(), "must stay inside the project") {
				t.Errorf("output.%s rejected with the wrong reason: %v", f.Name, err)
			}
		})
	}
	if checked == 0 {
		t.Fatal("no path fields found - the reflection walk is broken, not the guard")
	}
}

func TestOutputDefaultsPerKind(t *testing.T) {
	app, err := loadManifest(t, "")
	if err != nil {
		t.Fatal(err)
	}
	contracts, err := loadManifest(t, "output:\n  kind: contracts\n")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][2]string{
		"types":      {"./internal/types", "./gen/types"},
		"transport":  {"./internal/transport", "./internal/transport"},
		"routes":     {"./internal/routes", "./internal/routes"},
		"wiring":     {"./internal/wiring", "./internal/wiring"},
		"service":    {"./internal/service", "./internal/service"},
		"middleware": {"./internal/middleware", "./internal/middleware"},
		"config":     {"./config", "./config"},
		"pb":         {"./internal/pb", "./gen/pb"},
		"grpc":       {"./internal/grpc", "./internal/grpc"},
		"svccontext": {"./svccontext/svccontext.go", "./svccontext/svccontext.go"},
		"main":       {"./main.go", "./main.go"},
		"openapi":    {"./docs/openapi.yaml", "./docs/openapi.yaml"},
	}
	if len(outputKeys) != len(want) {
		t.Fatalf("outputKeys has %d keys, want %d", len(outputKeys), len(want))
	}
	for _, k := range outputKeys {
		if got := *k.field(&app.Output); got != want[k.name][0] {
			t.Errorf("%s default = %q, want %q", k.key(), got, want[k.name][0])
		}
		if got := *k.field(&contracts.Output); got != want[k.name][1] {
			t.Errorf("%s contracts default = %q, want %q", k.key(), got, want[k.name][1])
		}
	}
}

func TestOutputDisabledValue(t *testing.T) {
	_, err := loadManifest(t, "output:\n  wiring: \"-\"\n")
	want := `output.wiring cannot be "-" - other generated code imports this package, so there is nothing to disable; "-" is for output.main, output.openapi, output.pb and the event targets`
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
	cfg, err := loadManifest(t, "output:\n  main: \"-\"\n  openapi: \"-\"\n  pb: \"-\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Output.RuntimeDisabled() || !cfg.Output.OpenAPIDisabled() || !cfg.Output.PBDisabled() {
		t.Errorf("disabled = runtime %v, openapi %v, pb %v", cfg.Output.RuntimeDisabled(), cfg.Output.OpenAPIDisabled(), cfg.Output.PBDisabled())
	}
	if !(Output{}).OpenAPIDisabled() {
		t.Error("an unset output.openapi writes no document")
	}
}

func TestEventTargetCollidesWithAnOutputKey(t *testing.T) {
	cases := map[string]string{
		"output:\n  kind: contracts\n  types: ./gen\nevents:\n  targets:\n    - lang: go\n      out: ./gen\n": `output.types and events.targets[go].out both write to "gen"`,
		"events:\n  targets:\n    - lang: go\n      out: ./internal/transport/\n":                             `output.transport and events.targets[go].out both write to "internal/transport"`,
		"output:\n  pb: ./internal/events\n":                                                                  `output.pb and events.targets[go].out both write to "internal/events"`,
		"output:\n  types: ./internal/events\nevents:\n  targets:\n    - lang: go\n      out: \"-\"\n":        "",
		"output:\n  openapi: ./internal/events/openapi.yaml\n":                                                "",
	}
	for body, want := range cases {
		_, err := loadManifest(t, body)
		switch {
		case want == "" && err != nil:
			t.Errorf("%q: unexpected error %v", body, err)
		case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
			t.Errorf("%q: err = %v, want %q", body, err, want)
		}
	}
}

func TestOutputFileKeysCollideByDirectory(t *testing.T) {
	cases := map[string]string{
		"output:\n  svccontext: ./internal/types/svccontext.go\n": `output.types and output.svccontext both write to "internal/types"`,
		"output:\n  main: ./config/main.go\n":                     `output.config and output.main both write to "config"`,
		"output:\n  openapi: ./internal/types/openapi.yaml\n":     "",
	}
	for body, want := range cases {
		_, err := loadManifest(t, body)
		switch {
		case want == "" && err != nil:
			t.Errorf("%q: unexpected error %v", body, err)
		case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
			t.Errorf("%q: err = %v, want %q", body, err, want)
		}
	}
}
