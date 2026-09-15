package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// fullDesign reaches every gen-once template at once: a service with a
// route (service.tmpl), a declared middleware (middleware.tmpl), a
// published contract, and a second service consuming it - which is what
// puts the generated event library in front of the compiler.
const fullDesign = `package gate

middleware Guard

type Thing {
	id string
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
// render the branches fullDesign leaves out - main.tmpl alone imports a
// different set for each.
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

// fullManifest names every gen-once output explicitly, so a rename in the
// defaults surfaces here as a missing file rather than as silent coverage
// loss. The OpenAPI document lands under the project root, which is what
// puts main.go on its `//go:embed` branch.
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

// eventsOnlyDesign declares contracts and no route at all, the shape a
// contract library generates from.
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

// routesOnlyManifest turns off the documents as well, so main.go renders
// without the embed and without the bus.
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
	// goScaffolds maps each expected gen-once Go file to the template
	// that writes it, so a file that stops being emitted reads as lost
	// coverage rather than as a passing test.
	goScaffolds map[string]string
	// yamlScaffolds are gen-once files no Go toolchain reads.
	yamlScaffolds map[string]string
	// link runs `go build` rather than `go vet`: the command a user runs
	// on a fresh project, at roughly ten times the cost.
	link bool
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
		link: true,
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
		// A design with no HTTP method has no server to boot, so no
		// main.go is scaffolded - the deployable builds its own bus and
		// registers the descriptors itself. What IS written still has to
		// compile on its own.
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

// The gen-once scaffolds - main.go, the runtime config, svccontext, the
// service and consumer stubs, the middleware implementations - are skipped
// whenever the file already exists, so every fixture in this repo has held
// its copy since the day it was created and no later gen re-runs their
// templates. The one pass that does run them, renderGo, formats with
// format.Source, which parses rather than type-checks. A scaffold template
// can therefore emit Go that is syntactically valid and does not compile,
// with nothing between that and a user's first `craftgo gen`. These cases
// generate into empty directories, where the scaffolds are written for
// real, and hand each result to the compiler.
func TestScaffoldsCompile(t *testing.T) {
	root := repoRoot(t)
	for _, shape := range scaffoldShapes {
		t.Run(shape.name, func(t *testing.T) {
			dir := generateScaffoldProject(t, root, shape)

			check := exec.Command("go", "vet", "./...")
			if shape.link {
				check = exec.Command("go", "build", "./...")
			}
			check.Dir = dir
			check.Env = append(os.Environ(), "GOWORK="+filepath.Join(dir, "go.work"), "GOFLAGS=")
			if out, err := check.CombinedOutput(); err != nil {
				t.Fatalf("the generated project does not compile: %v\n%s", err, out)
			}

			// The YAML scaffolds reach no compiler, so a parse is what
			// stands behind them. example.config.yaml stops here: nothing
			// ever loads it, so its syntax is all that can be checked.
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
		})
	}
}

// configRoundTripTest decodes the generated config.yaml into the Config
// the generated config.go declares, refusing a key the struct has no field
// for. It runs inside the generated module because that is the only place
// the type exists. KnownFields is the test being stricter than the
// scaffold on purpose: config.Load unmarshals leniently, so at runtime a
// key that no longer matches is dropped in silence and the setting it was
// meant to carry reverts to its zero value.
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

// assertConfigRoundTrips runs configRoundTripTest against the generated
// project. Only the shape carrying the YAML scaffolds needs it: neither
// config template branches on the design - config.go.tmpl has no
// conditional at all and config.yaml.tmpl substitutes one service name -
// so a second shape would decode the same pair of files.
func assertConfigRoundTrips(t *testing.T, dir string) {
	t.Helper()
	mustWrite(t, filepath.Join(dir, "config"), "roundtrip_test.go", configRoundTripTest)
	run := exec.Command("go", "test", "./config/")
	run.Dir = dir
	run.Env = append(os.Environ(), "GOWORK="+filepath.Join(dir, "go.work"), "GOFLAGS=")
	if out, err := run.CombinedOutput(); err != nil {
		t.Errorf("config.yaml.tmpl and config.go.tmpl disagree: %v\n%s", err, out)
	}
}

// generateScaffoldProject runs gen over shape in a fresh directory and
// returns it, having confirmed every scaffold the shape claims is there.
func generateScaffoldProject(t *testing.T, root string, shape scaffoldShape) string {
	t.Helper()
	// The go command matches a workspace's `use` paths against the
	// directory it resolved, so both sides have to be the evaluated one -
	// on macOS t.TempDir() hands back a path under the /var symlink.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	goVersion := goDirective(t, root)
	design := filepath.Join(dir, "design")

	mustWrite(t, dir, "go.mod", "module craftgo.test/scaffoldgate\n\ngo "+goVersion+"\n")
	mustWrite(t, design, "craftgo.design.yaml", shape.manifest)
	mustWrite(t, design, "api.craftgo", shape.design)

	if err := runGen([]string{"-f", design, "-c", dir}); err != nil {
		t.Fatalf("runGen: %v", err)
	}
	for rel, tmpl := range shape.goScaffolds {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("%s wrote no %s, so this test no longer covers it: %v", tmpl, rel, err)
		}
	}

	// A workspace rather than requires: the generated module names no
	// dependency of its own, so the build resolves craftgo out of the repo
	// and everything else out of the root module's build list. Nothing is
	// fetched that building this repo has not already fetched.
	mustWrite(t, dir, "go.work", "go "+goVersion+"\n\nuse (\n\t.\n\t"+
		root+"\n\t"+filepath.Join(root, "pkg", "events")+"\n)\n")
	return dir
}

// repoRoot walks up to the module holding both go.mod and the separate
// pkg/events module, the pair a generated project's workspace has to name.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		_, mod := os.Stat(filepath.Join(dir, "go.mod"))
		_, events := os.Stat(filepath.Join(dir, "pkg", "events", "go.mod"))
		if mod == nil && events == nil {
			real, err := filepath.EvalSymlinks(dir)
			if err != nil {
				t.Fatal(err)
			}
			return real
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no craftgo module root above %s", dir)
		}
		dir = parent
	}
}

// goDirective reads the root module's language version so the generated
// module and its workspace track it instead of pinning a copy.
func goDirective(t *testing.T, root string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^go (\S+)$`).FindStringSubmatch(string(body))
	if m == nil {
		t.Fatalf("no go directive in %s/go.mod", root)
	}
	return strings.TrimSpace(m[1])
}
