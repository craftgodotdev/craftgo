package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRunInitWritesScaffold checks that init writes the manifest into the given
// folder, with no `package:` key, and that gen then runs from it without a
// warning.
func TestRunInitWritesScaffold(t *testing.T) {
	dir := t.TempDir()
	designFolder := filepath.Join(dir, "contracts", "v1")
	if err := runInit([]string{designFolder}); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(designFolder, "craftgo.design.yaml")); err != nil {
		t.Errorf("missing manifest: %v", err)
	}
	manifest, _ := os.ReadFile(filepath.Join(designFolder, "craftgo.design.yaml"))
	for _, line := range strings.Split(string(manifest), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "package:") {
			t.Errorf("manifest must NOT carry a `package:` field; module path now lives in go.mod:\n%s", manifest)
			break
		}
	}

	mustWrite(t, designFolder, "api.craftgo", minimalDesignDSL)
	mustWrite(t, dir, "go.mod", "module github.com/test/app\n\ngo 1.24\n")

	var err error
	_, stderr := captureOutput(t, func() { err = runGen([]string{"-f", designFolder, "-c", dir}) })
	if err != nil {
		t.Fatalf("runGen on scaffold: %v", err)
	}
	if strings.Contains(stderr, "craftgo: warning:") {
		t.Errorf("the init manifest raised warnings:\n%s", stderr)
	}
	for _, rel := range []string{
		"main.go",
		"internal/types/api/types.go",
		"internal/transport/probe_service/ping.go",
		"docs/openapi.yaml",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("missing generated %s: %v", rel, err)
		}
	}
}

// TestRunInitIdempotent checks that a second init leaves an edited manifest
// alone.
func TestRunInitIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := runInit([]string{dir}); err != nil {
		t.Fatal(err)
	}
	custom := "# USER EDIT - must survive re-init\npackage: github.com/edited/app\n"
	dest := filepath.Join(dir, "craftgo.design.yaml")
	if err := os.WriteFile(dest, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runInit([]string{dir}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != custom {
		t.Errorf("user edit was overwritten; got:\n%s", got)
	}
}

// minimalDesignDSL is a design with one type and one routed service.
const minimalDesignDSL = `package api

type Probe { id string }

service ProbeService {
    get Ping /ping {
        response   Probe
    }
}
`

// TestRunInitDefaultPath checks that init with no path writes the manifest
// into `design/` under the working directory.
func TestRunInitDefaultPath(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := runInit(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "design", "craftgo.design.yaml")); err != nil {
		t.Errorf("default-path init did not write manifest: %v", err)
	}
}

// TestRunGenContextOverridesProjectRoot checks that -c places the outputs under
// the given root, away from the design folder.
func TestRunGenContextOverridesProjectRoot(t *testing.T) {
	dir := t.TempDir()
	designFolder := filepath.Join(dir, "contracts", "v1")
	codeRoot := filepath.Join(dir, "services", "api")
	if err := os.MkdirAll(codeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, dir, "go.mod", "module github.com/test/monorepo\n\ngo 1.24\n")
	if err := runInit([]string{designFolder}); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	mustWrite(t, designFolder, "api.craftgo", minimalDesignDSL)
	if err := runGen([]string{"-f", designFolder, "-c", codeRoot}); err != nil {
		t.Fatalf("runGen: %v", err)
	}
	if _, err := os.Stat(filepath.Join(codeRoot, "internal", "types", "api", "types.go")); err != nil {
		t.Errorf("expected types under -c root, got: %v", err)
	}
	types, _ := os.ReadFile(filepath.Join(codeRoot, "internal", "types", "api", "types.go"))
	if !strings.Contains(string(types), "package api") {
		t.Errorf("generated types.go missing package decl:\n%s", types)
	}
}

// TestRunGenContextOverridesTheWalkUpRoot checks that -c places the outputs of
// a design found by the walk-up under the given root.
func TestRunGenContextOverridesTheWalkUpRoot(t *testing.T) {
	dir := t.TempDir()
	codeRoot := filepath.Join(dir, "services", "api")
	if err := os.MkdirAll(codeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, dir, "go.mod", "module github.com/test/monorepo\n\ngo 1.24\n")
	mustWrite(t, dir, "design/craftgo.design.yaml", "")
	mustWrite(t, dir, "design/api.craftgo", minimalDesignDSL)
	if err := runGen([]string{"-c", codeRoot, dir}); err != nil {
		t.Fatalf("runGen: %v", err)
	}
	if _, err := os.Stat(filepath.Join(codeRoot, "internal", "types", "api", "types.go")); err != nil {
		t.Errorf("expected types under -c root, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal")); err == nil {
		t.Error("gen wrote under the design folder's parent despite -c")
	}
}

// TestRunGenWithoutContextIgnoresWorkingDir checks that -f without -c
// generates under the design folder's parent, never the working directory.
func TestRunGenWithoutContextIgnoresWorkingDir(t *testing.T) {
	dir := t.TempDir()
	elsewhere := filepath.Join(dir, "elsewhere")
	lib := filepath.Join(dir, "lib")
	mustWrite(t, elsewhere, "go.mod", "module github.com/test/elsewhere\n\ngo 1.24\n")
	mustWrite(t, lib, "go.mod", "module github.com/test/lib\n\ngo 1.24\n")
	designFolder := filepath.Join(lib, "design")
	if err := runInit([]string{designFolder}); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	mustWrite(t, designFolder, "api.craftgo", minimalDesignDSL)

	t.Chdir(elsewhere)
	if err := runGen([]string{"-f", designFolder}); err != nil {
		t.Fatalf("runGen: %v", err)
	}
	if _, err := os.Stat(filepath.Join(lib, "internal", "types", "api", "types.go")); err != nil {
		t.Errorf("expected types under the design's parent: %v", err)
	}
	for _, rel := range []string{"internal", "docs", "svccontext", "main.go"} {
		if _, err := os.Stat(filepath.Join(elsewhere, rel)); err == nil {
			t.Errorf("gen wrote %s into the working directory", rel)
		}
	}
}

// TestRunGenWalkUpKeepsLegacyProjectRoot checks that `craftgo gen <path>` finds
// <path>/design and generates under the design folder's parent.
func TestRunGenWalkUpKeepsLegacyProjectRoot(t *testing.T) {
	dir := t.TempDir()
	designFolder := filepath.Join(dir, "design")
	if err := runInit([]string{designFolder}); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	mustWrite(t, designFolder, "api.craftgo", minimalDesignDSL)
	mustWrite(t, dir, "go.mod", "module github.com/test/legacy\n\ngo 1.24\n")
	if err := runGen([]string{dir}); err != nil {
		t.Fatalf("runGen: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "types", "api", "types.go")); err != nil {
		t.Errorf("legacy walk-up should land outputs at parent-of-manifest: %v", err)
	}
}

// TestRunGenMissingGoMod checks that gen fails without a go.mod and names
// `go mod init`.
func TestRunGenMissingGoMod(t *testing.T) {
	dir := t.TempDir()
	designFolder := filepath.Join(dir, "design")
	if err := runInit([]string{designFolder}); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	mustWrite(t, designFolder, "api.craftgo", minimalDesignDSL)
	err := runGen([]string{dir})
	if err == nil {
		t.Fatal("expected error when go.mod is missing")
	}
	if !strings.Contains(err.Error(), "go mod init") {
		t.Errorf("error must point at the fix; got: %v", err)
	}
}

// Each command reads its arguments alike: `-h` prints the command's whole
// usage, and a bad flag or argument is one error that main prints once,
// followed by that usage.
func TestCommandArguments(t *testing.T) {
	commands := map[string]func([]string) error{"init": runInit, "gen": runGen, "fmt": runFmt}
	for name, run := range commands {
		t.Run(name+" -h", func(t *testing.T) {
			var err error
			stdout, stderr := captureOutput(t, func() { err = run([]string{"-h"}) })
			if err != errHelpRequested || stderr != "" {
				t.Errorf("err = %v, stderr = %q", err, stderr)
			}
			if !strings.Contains(stdout, "craftgo "+name+" [") || !strings.Contains(stdout, commandUsage[name]) {
				t.Errorf("stdout lacks the %s usage:\n%s", name, stdout)
			}
		})
		t.Run(name+" -x", func(t *testing.T) {
			var err error
			stdout, stderr := captureOutput(t, func() { err = run([]string{"-x"}) })
			if err == nil || !strings.Contains(err.Error(), "flag provided but not defined: -x") {
				t.Errorf("err = %v", err)
			}
			if stdout != "" || stderr != "" {
				t.Errorf("the command printed: stdout %q, stderr %q", stdout, stderr)
			}
			var ue usageError
			if !errors.As(err, &ue) || ue.usage != commandUsage[name] {
				t.Errorf("err = %#v, want a usageError with the %s usage", err, name)
			}
		})
	}
	if err := runGen([]string{"-f", "design", "extra"}); err == nil || !strings.Contains(err.Error(), `"extra"`) {
		t.Errorf("gen -f design extra: err = %v", err)
	}
}

// The comment right above a declaration's keyword, below its decorators, is
// the generated Go doc.
func TestRunGenDocAboveTheKeyword(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "go.mod", "module github.com/test/app\n\ngo 1.24\n")
	mustWrite(t, dir, "design/craftgo.design.yaml", "")
	mustWrite(t, dir, "design/api.craftgo", "package api\n\n@deprecated\n// Order is the order.\ntype Order {\n\tid string\n}\n")
	if err := runGen([]string{"-f", filepath.Join(dir, "design")}); err != nil {
		t.Fatalf("runGen: %v", err)
	}
	types, _ := os.ReadFile(filepath.Join(dir, "internal", "types", "api", "types.go"))
	if !strings.Contains(string(types), "// Order is the order.\n") {
		t.Errorf("types.go lacks the doc:\n%s", types)
	}
}

// gen stops at a generic that instantiates itself with a growing argument,
// which no instance count can finish.
func TestRunGenRefusesAnExpandingGeneric(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "go.mod", "module github.com/test/app\n\ngo 1.24\n")
	mustWrite(t, dir, "design/craftgo.design.yaml", "")
	mustWrite(t, dir, "design/api.craftgo", `package api

type Tree<T> {
	v    T
	kids Tree<Tree<T>>[]
}

type Req { id string }

service S {
	get A /a { request Req  response Tree<int> }
}
`)
	done := make(chan error, 1)
	go func() { done <- runGen([]string{"-f", filepath.Join(dir, "design")}) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "Tree<Tree<T>>") {
			t.Errorf("runGen: err = %v, want the instantiation cycle at Tree<Tree<T>>", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runGen did not finish within 10s")
	}
}

// TestRunGenWarnsOnUnknownManifestKeys checks that gen names each manifest key
// it ignores on stderr and still generates.
func TestRunGenWarnsOnUnknownManifestKeys(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "go.mod", "module github.com/test/app\n\ngo 1.24\n")
	mustWrite(t, dir, "design/craftgo.design.yaml", `output:
  typs: ./gen/types
openapi:
  securitySchemes:
    bearer:
      type: http
      scheme: bearer
      description: Signed by the gateway
`)
	mustWrite(t, dir, "design/api.craftgo", minimalDesignDSL)
	var err error
	_, stderr := captureOutput(t, func() { err = runGen([]string{"-f", filepath.Join(dir, "design")}) })
	if err != nil {
		t.Fatalf("runGen: %v", err)
	}
	for _, key := range []string{"output.typs", "openapi.securitySchemes.bearer.description"} {
		if !strings.Contains(stderr, "craftgo: warning: "+key+" ") {
			t.Errorf("stderr does not warn about %s:\n%s", key, stderr)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "types", "api", "types.go")); err != nil {
		t.Errorf("gen stopped at the warning: %v", err)
	}
}

// TestGenNotesAMainWithoutTheDocsEmbed checks that a run after `--target go`
// names the gen-once main.go that does not embed the OpenAPI document now on
// disk, and that a main.go written beside the document, or serving one another
// file of its package embeds, gets no note.
func TestGenNotesAMainWithoutTheDocsEmbed(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "go.mod", "module github.com/test/app\n\ngo 1.24\n")
	mustWrite(t, dir, "design/craftgo.design.yaml", "")
	mustWrite(t, dir, "design/api.craftgo", minimalDesignDSL)
	gen := func(args ...string) string {
		t.Helper()
		var err error
		stdout, _ := captureOutput(t, func() {
			err = runGen(append(args, "-f", filepath.Join(dir, "design"), "-c", dir))
		})
		if err != nil {
			t.Fatalf("runGen %v: %v", args, err)
		}
		return stdout
	}
	gen("--target", "go")
	const note = "craftgo: ./main.go does not embed ./docs/openapi.yaml"
	if out := gen(); strings.Count(out, note) != 1 {
		t.Errorf("a full run after --target go does not note the main.go once:\n%s", out)
	}
	if err := os.Remove(filepath.Join(dir, "main.go")); err != nil {
		t.Fatal(err)
	}
	if out := gen(); strings.Contains(out, "does not embed") {
		t.Errorf("a main.go written beside the document gets the note:\n%s", out)
	}
	mainPath := filepath.Join(dir, "main.go")
	src, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	embed := "//go:embed docs/openapi.yaml\nvar openapiSpec []byte\n"
	if !strings.Contains(string(src), embed) {
		t.Fatalf("main.go does not embed the document:\n%s", src)
	}
	mustWrite(t, dir, "main.go", strings.Replace(string(src), embed, "", 1))
	mustWrite(t, dir, "docs.go", "package main\n\nimport _ \"embed\"\n\n"+embed)
	if out := gen(); strings.Contains(out, "does not embed") {
		t.Errorf("a main.go serving the document docs.go embeds gets the note:\n%s", out)
	}
}

// fmt on a design folder that holds only protos has nothing to format.
func TestRunFmtProtoOnlyDesign(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "design/craftgo.design.yaml", "")
	mustWrite(t, dir, "design/greet/greet.proto", "syntax = \"proto3\";\n")
	t.Chdir(dir)
	for _, args := range [][]string{nil, {"design"}, {"design/greet"}, {"-l"}} {
		if err := runFmt(args); err != nil {
			t.Errorf("fmt %q: %v", args, err)
		}
	}
	mustWrite(t, dir, "elsewhere/notes.txt", "")
	if err := runFmt([]string{"elsewhere"}); err == nil {
		t.Error("fmt on a folder outside every design: want the no-files error")
	}
}

// captureOutput runs fn and returns what it wrote to stdout and stderr.
func captureOutput(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	prevOut, prevErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	fn()
	os.Stdout, os.Stderr = prevOut, prevErr
	outW.Close()
	errW.Close()
	o, _ := io.ReadAll(outR)
	e, _ := io.ReadAll(errR)
	return string(o), string(e)
}

// TestRunInitRejectsLegacyPackageFlag checks that init rejects `-package`.
func TestRunInitRejectsLegacyPackageFlag(t *testing.T) {
	if err := runInit([]string{"-package", "github.com/test/app"}); err == nil {
		t.Error("expected error for removed -package flag")
	}
}

// TestRunGenMultiPackage checks that a root package using `shared.User` imports
// the shared types package, which gets its own types.go.
func TestRunGenMultiPackage(t *testing.T) {
	dir := t.TempDir()

	mustWrite(t, dir, "go.mod", "module github.com/test/multi\n\ngo 1.24\n")
	mustWrite(t, dir, "design/craftgo.design.yaml", "")

	mustWrite(t, dir, "design/api.craftgo", `package design
import "shared"

type Login {
    user shared.User
    note string
}

service Auth {
    post DoLogin /login {
        request   Login
        response  Login
    }
}
`)

	mustWrite(t, dir, "design/shared/user.craftgo", `package shared
type User {
    id   string
    name string
}
`)

	if err := runGen([]string{dir}); err != nil {
		t.Fatalf("runGen: %v", err)
	}

	rootTypes, err := os.ReadFile(filepath.Join(dir, "internal/types/design/types.go"))
	if err != nil {
		t.Fatalf("read root types.go: %v", err)
	}
	if !strings.Contains(string(rootTypes), "shared.User") {
		t.Errorf("root types.go missing `shared.User` reference:\n%s", rootTypes)
	}
	if !strings.Contains(string(rootTypes), `"github.com/test/multi/internal/types/shared"`) {
		t.Errorf("root types.go missing Go import for sibling package:\n%s", rootTypes)
	}

	subTypes, err := os.ReadFile(filepath.Join(dir, "internal/types/shared/types.go"))
	if err != nil {
		t.Fatalf("read sibling types.go: %v", err)
	}
	if !strings.Contains(string(subTypes), "package shared") {
		t.Errorf("sibling types.go missing `package shared`:\n%s", subTypes)
	}
	if !strings.Contains(string(subTypes), "type User struct") {
		t.Errorf("sibling types.go missing User decl:\n%s", subTypes)
	}
}

// TestRunGenSubpackageService checks that services in two packages each get
// handlers, both reach the umbrella routes and the OpenAPI document.
func TestRunGenSubpackageService(t *testing.T) {
	dir := t.TempDir()

	mustWrite(t, dir, "go.mod", "module github.com/test/multi\n\ngo 1.24\n")
	mustWrite(t, dir, "design/craftgo.design.yaml", "")
	mustWrite(t, dir, "design/api.craftgo", `package design
type Probe { id string }
service ProbeService {
    get Ping /ping {
        response   Probe
    }
}
`)
	mustWrite(t, dir, "design/auth/auth.craftgo", `package auth
type Cred { token string }
service AuthService {
    post Login /login {
        request   Cred
        response  Cred
    }
}
`)

	if err := runGen([]string{dir}); err != nil {
		t.Fatalf("runGen: %v", err)
	}

	for _, rel := range []string{
		"internal/transport/probe_service/ping.go",
		"internal/transport/auth_service/login.go",
		"internal/types/design/types.go",
		"internal/types/auth/types.go",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}

	umbrella, err := os.ReadFile(filepath.Join(dir, "internal/routes/routes.go"))
	if err != nil {
		t.Fatalf("read umbrella: %v", err)
	}
	if !strings.Contains(string(umbrella), "probe_service") {
		t.Errorf("umbrella missing probe_service import:\n%s", umbrella)
	}
	if !strings.Contains(string(umbrella), "auth_service") {
		t.Errorf("umbrella missing auth_service import:\n%s", umbrella)
	}

	spec, err := os.ReadFile(filepath.Join(dir, "docs/openapi.yaml"))
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	if !strings.Contains(string(spec), "/ping") {
		t.Errorf("openapi missing /ping route:\n%s", spec)
	}
	if !strings.Contains(string(spec), "/login") {
		t.Errorf("openapi missing /login route:\n%s", spec)
	}
}

// TestRunGenCrossPackageRequestResponse checks that a method whose request and
// response are `shared` types imports them as `shared` and drops the unused
// local `types` import.
func TestRunGenCrossPackageRequestResponse(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "go.mod", "module github.com/test/cross\n\ngo 1.24\n")
	mustWrite(t, dir, "design/craftgo.design.yaml", "")
	mustWrite(t, dir, "design/api.craftgo", `package design
import "shared"

service Auth {
    post Login /login {
        request   shared.Cred
        response  shared.Token
    }
}
`)
	mustWrite(t, dir, "design/shared/types.craftgo", `package shared
type Cred  { user string  pass string }
type Token { value string }
`)

	if err := runGen([]string{dir}); err != nil {
		t.Fatalf("runGen: %v", err)
	}

	handler, err := os.ReadFile(filepath.Join(dir, "internal/transport/auth/login.go"))
	if err != nil {
		t.Fatalf("read handler: %v", err)
	}
	hs := string(handler)
	if !strings.Contains(hs, "\t\"github.com/test/cross/internal/types/shared\"\n") {
		t.Errorf("handler missing cross-pkg import:\n%s", hs)
	}
	if !strings.Contains(hs, "var req shared.Cred") {
		t.Errorf("handler missing `var req shared.Cred`:\n%s", hs)
	}
	if strings.Contains(hs, `types "`) {
		t.Errorf("handler should not import the canonical types alias when request is cross-pkg:\n%s", hs)
	}

	logic, err := os.ReadFile(filepath.Join(dir, "internal/service/auth/login.go"))
	if err != nil {
		t.Fatalf("read logic: %v", err)
	}
	ls := string(logic)
	if !strings.Contains(ls, "\t\"github.com/test/cross/internal/types/shared\"\n") {
		t.Errorf("logic missing cross-pkg import:\n%s", ls)
	}
	if !strings.Contains(ls, "(req *shared.Cred)") {
		t.Errorf("logic signature wrong; want `req *shared.Cred`:\n%s", ls)
	}
	if !strings.Contains(ls, "*shared.Token") {
		t.Errorf("logic response type wrong:\n%s", ls)
	}
	if strings.Contains(ls, `types "`) {
		t.Errorf("logic should not import the canonical types alias when both sides cross-pkg:\n%s", ls)
	}
}
