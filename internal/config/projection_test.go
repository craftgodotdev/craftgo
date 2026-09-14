package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// manifestAt lays out a manifest folder under dir and returns the path of
// the manifest itself.
func manifestAt(t *testing.T, dir, rel, body string) string {
	t.Helper()
	folder := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(folder, Filename)
	writeFile(t, path, body)
	return path
}

// source lays out a design a projection can point at: a manifest and one
// `.craftgo` file beside it.
func source(t *testing.T, dir, rel, body string) string {
	t.Helper()
	path := manifestAt(t, dir, rel, body)
	writeFile(t, filepath.Join(filepath.Dir(path), "api.craftgo"), "package shop\n")
	return path
}

// A projection generates from a design source it does not contain: the
// `.craftgo` files are the source's, and so is the contract half - where
// the payload types and the event artefacts land.
func TestProjectionTakesTheContractHalfFromItsSource(t *testing.T) {
	dir := t.TempDir()
	source(t, dir, "contracts/design", "output:\n  kind: contracts\n  fileCase: kebab\n")
	path := manifestAt(t, dir, "services/consumer/design", `design:
  from: ../../../contracts/design
  root: ../../../contracts
output:
  services: [shop.Billing]
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.IsProjection() {
		t.Fatal("a manifest naming a design source is a projection")
	}
	if want := filepath.Join(dir, "contracts", "design"); cfg.SourceDesign != want {
		t.Errorf("design source = %q, want %q", cfg.SourceDesign, want)
	}
	if want := filepath.Join(dir, "contracts"); cfg.LibraryRoot("/unused") != want {
		t.Errorf("library root = %q, want %q", cfg.LibraryRoot("/unused"), want)
	}
	// The source is a contracts project, so its types default outside
	// `internal/`; the projection reads that, not its own default.
	if cfg.Output.Types != "./gen/types" {
		t.Errorf("output.types = %q, want the source's ./gen/types", cfg.Output.Types)
	}
	if cfg.Output.FileCase != FileCaseKebab {
		t.Errorf("output.fileCase = %q, want the source's kebab", cfg.Output.FileCase)
	}
	if target, ok := cfg.Events.TargetFor(LangGo); !ok || target.Out != "./gen/events" {
		t.Errorf("events target = %+v, want the source's ./gen/events", target)
	}
	// `kind` is NOT inherited: the source generates the contract half,
	// the projection the application around it.
	if cfg.Output.ContractsOnly() {
		t.Error("a projection of a contracts design is still an application")
	}
	if cfg.Output.Transport != "./internal/transport" {
		t.Errorf("the application half keeps its own defaults, got %q", cfg.Output.Transport)
	}
}

// `@security(name)` resolves against the manifest's schemes, so a
// projection analysing a design it did not write needs the design's.
func TestProjectionInheritsOpenAPIMetadataItDoesNotState(t *testing.T) {
	dir := t.TempDir()
	source(t, dir, "contracts/design", `openapi:
  title: Shop
  basePath: /api
  securitySchemes:
    Bearer:
      type: http
      scheme: bearer
`)
	path := manifestAt(t, dir, "services/api/design", `design:
  from: ../../../contracts/design
  root: ../../../contracts
openapi:
  title: Shop API
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OpenAPI.Title != "Shop API" {
		t.Errorf("what the projection states wins, got %q", cfg.OpenAPI.Title)
	}
	if cfg.OpenAPI.BasePath != "/api" {
		t.Errorf("basePath = %q, want the design's", cfg.OpenAPI.BasePath)
	}
	if _, ok := cfg.OpenAPI.SecuritySchemes["Bearer"]; !ok {
		t.Errorf("the design's security schemes must reach the projection: %v", cfg.OpenAPI.SecuritySchemes)
	}
	if want := filepath.Join(dir, "contracts"); cfg.Library.Root != want {
		t.Errorf("library root = %q, want %q", cfg.Library.Root, want)
	}
}

// A manifest that both names a design source and holds a design of its
// own is ambiguous: craftgo would have to guess which one the project
// generates from, and the answer is not a merge.
func TestManifestCannotNameADesignAndHoldOne(t *testing.T) {
	dir := t.TempDir()
	source(t, dir, "contracts/design", "")
	path := manifestAt(t, dir, "services/consumer/design", `design:
  from: ../../../contracts/design
  root: ../../../contracts
`)
	writeFile(t, filepath.Join(filepath.Dir(path), "local.craftgo"), "package local\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("want a rejection, got none")
	}
	if !strings.Contains(err.Error(), "local.craftgo") || !strings.Contains(err.Error(), "design.from") {
		t.Errorf("the error must name both halves of the ambiguity, got: %v", err)
	}
}

// The contract half is the design's, and every deployable of one design
// must write it identically. A projection stating those keys would be
// stating a second answer.
func TestProjectionRejectsTheKeysItsSourceOwns(t *testing.T) {
	dir := t.TempDir()
	source(t, dir, "contracts/design", "")
	for _, tc := range []struct{ name, body, key string }{
		{"types", "output:\n  types: ./gen/types\n", "output.types"},
		{"fileCase", "output:\n  fileCase: kebab\n", "output.fileCase"},
		{"targets", "events:\n  targets:\n    - lang: go\n      out: ./gen/events\n", "events.targets"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := manifestAt(t, dir, "services/"+tc.name+"/design",
				"design:\n  from: ../../../contracts/design\n  root: ../../../contracts\n"+tc.body)
			_, err := Load(path)
			if err == nil {
				t.Fatalf("%s is the design source's to set", tc.key)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Errorf("the error must name the key, got: %v", err)
			}
		})
	}
}

// A project root without a design source is this project's own, and that
// is the `-c` flag - a manifest saying otherwise is misreading the block.
func TestDesignRootWithoutASourceIsRejected(t *testing.T) {
	dir := t.TempDir()
	path := manifestAt(t, dir, "design", "design:\n  root: ../contracts\n")
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "design.root") {
		t.Errorf("want a design.root rejection, got: %v", err)
	}
}

// A projection reads one design. Pointing at another projection would
// leave the `.craftgo` files a hop further away with nothing saying so.
func TestProjectionCannotNameAnotherProjection(t *testing.T) {
	dir := t.TempDir()
	source(t, dir, "contracts/design", "")
	manifestAt(t, dir, "services/first/design", "design:\n  from: ../../../contracts/design\n  root: ../../../contracts\n")
	path := manifestAt(t, dir, "services/second/design", "design:\n  from: ../../first/design\n  root: ../../first\n")

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "projection") {
		t.Errorf("want a rejection naming the projection, got: %v", err)
	}
}

// design.from names a folder, and a typo in it must surface where it is
// written rather than as "no .craftgo files found" three layers down.
func TestProjectionRejectsAMissingSource(t *testing.T) {
	dir := t.TempDir()
	path := manifestAt(t, dir, "services/consumer/design", "design:\n  from: ../../../contracts/design\n  root: ../../../contracts\n")
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "design.from") {
		t.Errorf("want a design.from rejection, got: %v", err)
	}
}

// The entry names a service through the package declaring it. Whether
// that service exists is checked against the analysed design, which can
// name the near misses; the shape is checked here.
func TestServiceSelectionMustNamePackageAndService(t *testing.T) {
	dir := t.TempDir()
	for _, entry := range []string{"Billing", "shop.", ".Billing", "shop.a.Billing"} {
		path := manifestAt(t, dir, "design", "output:\n  services: ["+entry+"]\n")
		_, err := Load(path)
		if err == nil || !strings.Contains(err.Error(), "<package>.<Service>") {
			t.Errorf("output.services %q: want a shape rejection, got: %v", entry, err)
		}
	}
	path := manifestAt(t, dir, "design", "output:\n  services: [shop.Billing, billing.Settlements]\n")
	if _, err := Load(path); err != nil {
		t.Errorf("a `<package>.<Service>` list is the accepted shape: %v", err)
	}
}

// The source is a craftgo project: its manifest is what places the
// contract half the projection shares, so a folder without one cannot be
// a design source.
func TestProjectionRejectsASourceWithoutAManifest(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "contracts", "design"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "contracts", "design", "api.craftgo"), "package shop\n")
	path := manifestAt(t, dir, "services/consumer/design", "design:\n  from: ../../../contracts/design\n  root: ../../../contracts\n")

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), Filename) {
		t.Errorf("want a rejection naming the missing manifest, got: %v", err)
	}
}

// Two manifests naming each other are a cycle. Loading one pulls in the
// other, so it has to be reported rather than followed until the stack
// runs out.
func TestProjectionCycleIsReported(t *testing.T) {
	dir := t.TempDir()
	manifestAt(t, dir, "a", "design:\n  from: ../b\n  root: ..\n")
	path := manifestAt(t, dir, "b", "design:\n  from: ../a\n  root: ..\n")

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Errorf("want a cycle rejection, got: %v", err)
	}
}

// The project root a design generates with is a `-c` argument, not
// something craftgo can read off the folder: a design at
// `contracts/upstream/design` may be generated with `contracts` as its
// root as readily as with `contracts/upstream`. Guessing it produces an
// import path that does not exist, so it is stated.
func TestProjectionRequiresTheSourcesProjectRoot(t *testing.T) {
	dir := t.TempDir()
	source(t, dir, "contracts/upstream/design", "")
	path := manifestAt(t, dir, "services/consumer/design", "design:\n  from: ../../../contracts/upstream/design\n")

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "design.root is required") {
		t.Errorf("want a design.root rejection, got: %v", err)
	}

	// Stated, it is used verbatim - the parent it is NOT.
	path = manifestAt(t, dir, "services/other/design",
		"design:\n  from: ../../../contracts/upstream/design\n  root: ../../../contracts\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "contracts"); cfg.Library.Root != want {
		t.Errorf("library root = %q, want the stated %q", cfg.Library.Root, want)
	}
}

// A typo in design.root surfaces where it is written rather than as a
// broken import in generated code.
func TestProjectionRejectsAMissingProjectRoot(t *testing.T) {
	dir := t.TempDir()
	source(t, dir, "contracts/design", "")
	path := manifestAt(t, dir, "services/consumer/design",
		"design:\n  from: ../../../contracts/design\n  root: ../../../contract\n")
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "design.root") {
		t.Errorf("want a design.root rejection, got: %v", err)
	}
}

// A manifest written by a tool, or one pointing at a design outside its
// own tree, gives the paths absolute. They name the folder itself; only
// a relative path is read from the manifest's own folder.
func TestProjectionAcceptsAbsoluteDesignPaths(t *testing.T) {
	dir := t.TempDir()
	source(t, dir, "contracts/design", "")
	relative, err := Load(manifestAt(t, dir, "services/relative/design",
		"design:\n  from: ../../../contracts/design\n  root: ../../../contracts\n"))
	if err != nil {
		t.Fatal(err)
	}
	absolute, err := Load(manifestAt(t, dir, "services/absolute/design",
		"design:\n  from: "+filepath.ToSlash(filepath.Join(dir, "contracts", "design"))+
			"\n  root: "+filepath.ToSlash(filepath.Join(dir, "contracts"))+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if absolute.SourceDesign != relative.SourceDesign {
		t.Errorf("design source = %q, want %q", absolute.SourceDesign, relative.SourceDesign)
	}
	if absolute.Library.Root != relative.Library.Root {
		t.Errorf("library root = %q, want %q", absolute.Library.Root, relative.Library.Root)
	}
}
