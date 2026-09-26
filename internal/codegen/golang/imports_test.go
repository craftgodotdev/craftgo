package golang

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// groupedSrc is a design whose files each import the standard library, the runtime and the
// project: a handler and stub naming another package's types, its routes and wiring, and the
// types, errors and events of a package naming them too.
var groupedSrc = []string{`package app
import "shared"
error NotFound Missing { id shared.ID @header("X-Id") }
event Seen { payload shared.Item }
service Items {
	@timeout(5s)
	post Find /items/{id} { request shared.Item  response shared.Item }
}`, `package shared
scalar ID string @minLength(1)
type Item { id ID  at datetime }`}

// importGroupsOf returns the import specs of src's import declaration, group by group.
func importGroupsOf(t *testing.T, src string) [][]string {
	t.Helper()
	_, rest, ok := strings.Cut(src, "\nimport (\n")
	if !ok {
		t.Fatalf("no import declaration:\n%s", src)
	}
	block, _, _ := strings.Cut(rest, "\n)\n")
	var groups [][]string
	for g := range strings.SplitSeq(block, "\n\n") {
		groups = append(groups, strings.Split(strings.TrimSpace(g), "\n"))
	}
	return groups
}

// Every generated file imports the standard library, then other modules, then the project's
// own module, a blank line between the groups: the module is recognised by its path, dotted or
// not.
func TestImportsGroupStdOtherModulesProject(t *testing.T) {
	for _, module := range []string{"github.com/example/app", "myapp"} {
		t.Run(module, func(t *testing.T) {
			proj := analyzeProject(t, groupedSrc...)
			cfg := sampleConfig()
			cfg.Package = module
			root := t.TempDir()
			if err := Generate(proj, nil, cfg, root); err != nil {
				t.Fatal(err)
			}
			if err := GenerateEventTarget(proj, cfg, root, goEventsOut); err != nil {
				t.Fatal(err)
			}
			for _, rel := range []string{
				"internal/types/app/errors.go",
				"internal/types/app/validate.go",
				"internal/types/shared/types.go",
				"internal/transport/items/find.go",
				"internal/service/items/find.go",
				"internal/routes/items/routes.go",
				"internal/routes/routes.go",
				"internal/wiring/wiring.go",
				goEventsOut + "/app/events.go",
			} {
				body, err := os.ReadFile(filepath.Join(root, rel))
				if err != nil {
					t.Fatal(err)
				}
				// 0 is the standard library, 1 another module, 2 the project.
				group := func(spec string) int {
					path, _ := strconv.Unquote(spec[strings.IndexByte(spec, '"'):])
					first, _, _ := strings.Cut(path, "/")
					switch {
					case path == module || strings.HasPrefix(path, module+"/"):
						return 2
					case !strings.Contains(first, "."):
						return 0
					}
					return 1
				}
				last := -1
				for _, specs := range importGroupsOf(t, string(body)) {
					g := group(specs[0])
					for _, spec := range specs {
						if group(spec) != g || g <= last {
							t.Errorf("%s: %s is out of its group:\n%s", rel, spec, body)
						}
					}
					last = g
				}
			}
		})
	}
}

// importClashSrc names packages after identifiers the templates bind (`server` in the handler,
// `log` and `context` in the stub, `fmt` in the event file) and passes a builtin that lives in
// another package as a type argument.
var importClashSrc = []string{`package app
import "server"
import "log"
import "context"
type Page<T> { items T[] }
event Ticked { payload Page<datetime> }
service Auth {
	post Login /login/{id} { request server.Cred  response log.Out }
	get Ctx /ctx { request context.Q  response context.Out }
	get Now /now { response Page<datetime> }
}`, `package server
scalar ID string
enum Kind { A B }
type Cred {
	id   ID
	kind Kind @query @default(A)
	user string
}`, `package log
type Out { ok bool }`, `package context
type Q { n int @default(3) }
type Out { ok bool }`, `package fmt
type Item { name string @minLength(1) }`, `package orders
import "fmt"
event Batch { payload fmt.Item[] }`}

// typeFilesClashSrc names packages after the packages types.go, errors.go and validate.go import
// and after the receiver validate.go binds, and names each where its file needs that import.
var typeFilesClashSrc = []string{`package app
import "time"
import "multipart"
import "wire"
import "json"
import "http"
import "strconv"
import "fmt"
import "utf8"
import "v"
type X {
	at    datetime
	t     time.Item
	f     file?
	m     multipart.Item
	r     bytes @format(raw)
	w     wire.Item
	s     string @minLength(1)
	codes fmt.Code[] @uniqueItems
	runes utf8.Code[] @uniqueItems
	vs    v.Code[] @uniqueItems
}
error TooManyRequests Slow {
	after int @header("Retry-After")
	j     json.Item
	h     http.Item
	c     strconv.Item
}`, `package time
type Item { n string }`, `package multipart
type Item { n string }`, `package wire
type Item { n string }`, `package json
type Item { n string }`, `package http
type Item { n string }`, `package strconv
type Item { n string }`, `package fmt
scalar Code string`, `package utf8
scalar Code string`, `package v
scalar Code string`}

// types.go, errors.go and validate.go import each package they name once, under a name nothing
// else in the file binds.
func TestTypeFilesBindEachNameOnce(t *testing.T) {
	proj := analyzeProject(t, typeFilesClashSrc...)
	dir := t.TempDir()
	pkg, r := proj.Packages["app"], buildProjectResolver(proj, sampleConfig(), "app")
	for _, gen := range []func(*semantic.Package, string, *projectResolver) error{generateTypes, generateErrors, generateValidators} {
		if err := gen(pkg, dir, r); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"types.go", "errors.go", "validate.go"} {
		body, err := os.ReadFile(filepath.Join(dir, "app", file))
		if err != nil {
			t.Fatal(err)
		}
		t.Run(file, func(t *testing.T) { mustParseGo(t, string(body)) })
		if file == "validate.go" {
			mustContainAll(t, string(body), "map[v2.Code]struct{}")
		}
	}
}

// Every handler, stub and event file imports each package it names once, under a name nothing
// else in the file binds.
func TestImportsBindEachNameOnce(t *testing.T) {
	proj := analyzeProject(t, importClashSrc...)
	cfg := sampleConfig()
	root := t.TempDir()
	pkg, r := proj.Packages["app"], buildProjectResolver(proj, cfg, "app")
	if err := generateTransport(pkg, cfg, root, r); err != nil {
		t.Fatal(err)
	}
	if err := generateService(pkg, cfg, root, r); err != nil {
		t.Fatal(err)
	}
	if err := GenerateEventTarget(proj, cfg, root, goEventsOut); err != nil {
		t.Fatal(err)
	}
	files := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files++
		rel, _ := filepath.Rel(root, path)
		t.Run(filepath.ToSlash(rel), func(t *testing.T) { mustParseGo(t, string(body)) })
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files < 8 {
		t.Fatalf("generated %d files, too few to cover the handler, stub and event templates", files)
	}
}
