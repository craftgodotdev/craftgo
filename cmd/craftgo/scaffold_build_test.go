package main

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// fullDesign declares a middleware, an event, a raw bytes field and a routed
// service.
const fullDesign = `package gate

middleware Guard

type Thing {
	id      string
	payload bytes @format(raw)
}

type GetReq {
	id string @path
}

type ThingCreated {
	id string
}

event ThingCreated {
	payload ThingCreated
}

@middlewares(Guard)
service ThingService {
	get GetThing /things/{id} {
		request  GetReq
		response Thing
	}
}
`

// routesOnlyDesign declares no event and no middleware, so the scaffolds
// render the branches fullDesign skips.
const routesOnlyDesign = `package gate

type Thing {
	id string
}

type GetReq {
	id string @path
}

service ThingService {
	get GetThing /things/{id} {
		request  GetReq
		response Thing
	}
}
`

// fullManifest names every gen-once output; its OpenAPI document under the
// project root puts main.go on its embed branch.
const fullManifest = `output:
  types:      ./internal/types
  transport:  ./internal/transport
  routes:     ./internal/routes
  service:    ./internal/service
  middleware: ./internal/middleware
  svccontext: ./svccontext/svccontext.go
  main:       ./main.go
  config:     ./config
  openapi:    ./docs/openapi.yaml
openapi:
  title:   Scaffold Gate
  version: 1.0.0
events:
  targets:
    - lang: go
      out: ./internal/events
`

// eventsOnlyDesign declares an event and no route.
const eventsOnlyDesign = `package gate

type ThingCreated {
	id string
}

event ThingCreated {
	payload ThingCreated
}
`

const eventsOnlyManifest = `output:
  types:      ./internal/types
  transport:  ./internal/transport
  routes:     ./internal/routes
  service:    ./internal/service
  svccontext: ./svccontext/svccontext.go
  main:       ./main.go
  config:     ./config
  openapi:    "-"
events:
  targets:
    - lang: go
      out: ./internal/events
`

// routesOnlyManifest turns the OpenAPI document off, so main.go renders
// without the embed.
const routesOnlyManifest = `output:
  types:      ./internal/types
  transport:  ./internal/transport
  routes:     ./internal/routes
  service:    ./internal/service
  svccontext: ./svccontext/svccontext.go
  main:       ./main.go
  config:     ./config
  openapi:    "-"
`

// scaffoldShape is one generated project: the design that produces it,
// the gen-once files it must contain, and whether the compiler links it
// or only type-checks it.
type scaffoldShape struct {
	name     string
	manifest string
	design   string
	// goScaffolds maps each expected gen-once Go file to the template that
	// writes it.
	goScaffolds map[string]string
	// yamlScaffolds are gen-once files no Go toolchain reads.
	yamlScaffolds map[string]string
	// link checks with `go build`; otherwise with `go vet`.
	link bool
	// typesRoundTrip runs a JSON round trip against the generated types package.
	typesRoundTrip bool
}

var scaffoldShapes = []scaffoldShape{
	{
		name:     "events, middleware and docs",
		manifest: fullManifest,
		design:   fullDesign,
		goScaffolds: map[string]string{
			"main.go":                  "main.tmpl",
			"config/config.go":         "config.go.tmpl",
			"svccontext/svccontext.go": "svccontext.go.tmpl",
			"internal/service/thing_service/get_thing.go": "service.tmpl",
			"internal/middleware/guard_middleware.go":     "middleware.tmpl",
		},
		yamlScaffolds: map[string]string{
			"config/config.yaml":         "config.yaml.tmpl",
			"config/example.config.yaml": "example.config.yaml.tmpl",
		},
		link:           true,
		typesRoundTrip: true,
	},
	{
		name:     "routes only",
		manifest: routesOnlyManifest,
		design:   routesOnlyDesign,
		goScaffolds: map[string]string{
			"main.go":                  "main.tmpl",
			"config/config.go":         "config.go.tmpl",
			"svccontext/svccontext.go": "svccontext.go.tmpl",
			"internal/service/thing_service/get_thing.go": "service.tmpl",
		},
	},
	{
		// With no HTTP method no main.go is written; the rest must still compile.
		name:     "events only",
		manifest: eventsOnlyManifest,
		design:   eventsOnlyDesign,
		goScaffolds: map[string]string{
			"internal/events/gate/events.go": "events.tmpl",
			"internal/wiring/wiring.go":      "wiring.tmpl",
		},
		link: true,
	},
}

// TestScaffoldsCompile generates each shape into an empty directory, so the
// gen-once scaffolds are written, and compiles the result.
func TestScaffoldsCompile(t *testing.T) {
	root := repoRoot(t)
	for _, shape := range scaffoldShapes {
		t.Run(shape.name, func(t *testing.T) {
			dir := generateScaffoldProject(t, root, shape)

			verb := "vet"
			if shape.link {
				verb = "build"
			}
			if out, err := goIn(dir, verb, "./..."); err != nil {
				t.Fatalf("the generated project does not compile: %v\n%s", err, out)
			}

			for rel, tmpl := range shape.yamlScaffolds {
				body, err := os.ReadFile(filepath.Join(dir, rel))
				if err != nil {
					t.Fatalf("%s wrote no %s, so this test no longer covers it: %v", tmpl, rel, err)
				}
				var doc map[string]any
				if err := yaml.Unmarshal(body, &doc); err != nil {
					t.Errorf("%s emits invalid YAML in %s: %v", tmpl, rel, err)
				}
			}
			if len(shape.yamlScaffolds) > 0 {
				assertConfigRoundTrips(t, dir)
			}
			if shape.typesRoundTrip {
				assertRawFieldRoundTrips(t, dir)
			}
		})
	}
}

// configRoundTripTest decodes the generated config.yaml into the generated
// Config and fails on any key the struct lacks.
const configRoundTripTest = `package config

import (
	"bytes"
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestGeneratedConfigRoundTrips(t *testing.T) {
	body, err := os.ReadFile("config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(body))
	dec.KnownFields(true)
	var c Config
	if err := dec.Decode(&c); err != nil {
		t.Fatalf("config.yaml does not fit the Config config.go declares: %v", err)
	}
}
`

// assertConfigRoundTrips runs configRoundTripTest in the generated project.
func assertConfigRoundTrips(t *testing.T, dir string) {
	t.Helper()
	mustWrite(t, filepath.Join(dir, "config"), "roundtrip_test.go", configRoundTripTest)
	if out, err := goIn(dir, "test", "./config/"); err != nil {
		t.Errorf("config.yaml.tmpl and config.go.tmpl disagree: %v\n%s", err, out)
	}
}

// rawRoundTripTest checks that a `bytes @format(raw)` field re-encodes the
// exact bytes it decoded.
const rawRoundTripTest = `package gate

import (
	"encoding/json"
	"testing"
)

func TestRawFieldRoundTripsByteForByte(t *testing.T) {
	const raw = ` + "`" + `{"explicit":null,"big":12345678901234567890,"trailing":1.50}` + "`" + `
	in := ` + "`" + `{"id":"t-1","payload":` + "`" + ` + raw + ` + "`" + `}` + "`" + `

	var got Thing
	if err := json.Unmarshal([]byte(in), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(got.Payload) != raw {
		t.Fatalf("payload decoded to %s, want the bytes that arrived: %s", got.Payload, raw)
	}
	out, err := json.Marshal(&got)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if string(out) != in {
		t.Fatalf("re-encoded to %s, want %s", out, in)
	}
}
`

// assertRawFieldRoundTrips runs rawRoundTripTest in the generated types package.
func assertRawFieldRoundTrips(t *testing.T, dir string) {
	t.Helper()
	pkg := filepath.Join("internal", "types", "gate")
	mustWrite(t, filepath.Join(dir, pkg), "roundtrip_test.go", rawRoundTripTest)
	if out, err := goIn(dir, "test", "./"+filepath.ToSlash(pkg)+"/"); err != nil {
		t.Errorf("a raw field does not carry its bytes through unchanged: %v\n%s", err, out)
	}
}

// generateScaffoldProject runs gen over shape in a fresh directory and
// returns it, having confirmed every scaffold the shape claims is there.
func generateScaffoldProject(t *testing.T, root string, shape scaffoldShape) string {
	t.Helper()
	// go matches workspace `use` paths against the resolved directory, and on
	// macOS t.TempDir() is under a symlink.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	goVersion := goDirective(t, root)
	design := filepath.Join(dir, "design")

	mustWrite(t, dir, "go.mod", "module craftgo.test/scaffoldgate\n\ngo "+goVersion+"\n")
	mustWrite(t, design, "craftgo.design.yaml", shape.manifest)
	mustWrite(t, design, "api.craftgo", shape.design)

	genProject(t, dir)
	for rel, tmpl := range shape.goScaffolds {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("%s wrote no %s, so this test no longer covers it: %v", tmpl, rel, err)
		}
	}
	writeWorkspace(t, dir, root, goVersion)
	return dir
}
