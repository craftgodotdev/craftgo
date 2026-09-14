// Package config loads and validates `craftgo.design.yaml`, the project
// manifest read by every CLI command.
//
// The manifest lives **inside** the design folder. A repo-relative layout
// looks like:
//
//	myapp/
//	├── design/
//	│   ├── craftgo.design.yaml
//	│   └── api.craftgo
//	└── internal/...                  (generated)
//
// The directory holding the manifest is the **design root**; its parent is
// the **project root** that every `output:` path is resolved against. This
// arrangement keeps each design folder fully self-contained, which is the
// pre-requisite for a single repo to host multiple craftgo modules
// (monorepo scenario).
package config

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the in-memory shape of `craftgo.design.yaml`. Field tags match
// the camelCase keys documented in the project README.
type Config struct {
	Design  Design  `yaml:"design"`
	Output  Output  `yaml:"output"`
	OpenAPI OpenAPI `yaml:"openapi"`
	Events  Events  `yaml:"events"`

	// Package is the Go import path prefix every generated file uses
	// for its imports - the equivalent of <module>/<relPathFromGoMod>
	// for the project root. Not loaded from YAML: populated at gen
	// time by [ResolveModulePath], which walks up from the project
	// root looking for a go.mod and computes the effective import
	// path. The manifest carries no module field; go.mod's `module`
	// line is the sole source of truth.
	Package string `yaml:"-"`

	// SourceDesign is the absolute folder holding the `.craftgo` files
	// this manifest generates from. Not loaded from YAML: resolved from
	// `design.from` when the manifest loads, and empty for a manifest
	// whose design sits beside it.
	SourceDesign string `yaml:"-"`

	// ManifestDir is the absolute folder this manifest was loaded from.
	// Not loaded from YAML. It is what a diagnostic names when two
	// manifests generate the same file - the design they read may be one
	// and the same, but the file the user edits is this.
	ManifestDir string `yaml:"-"`

	// Library locates the contract half - the payload types and the
	// event artefacts every deployable of one design shares. Not loaded
	// from YAML: Root resolves from `design.root` when the manifest
	// loads, Package at gen time the way [Config.Package] does. Both are
	// empty for a manifest that holds its own design, where the contract
	// half belongs to this project.
	Library Library `yaml:"-"`
}

// Design names the design source a manifest generates from. A manifest
// that omits the block holds its own `.craftgo` files, the 1:1 layout; a
// manifest that names one is a PROJECTION of a design it does not
// contain, and several projections may share a single source.
//
// Both paths are relative to the folder holding the manifest.
type Design struct {
	// From is the folder holding the `.craftgo` files.
	From string `yaml:"from"`
	// Root is the project root the source design's own output paths
	// resolve against - the `craftgo gen -c` it is generated with.
	// Required beside From: it is a caller's argument, so there is
	// nothing on the folder to read it off.
	Root string `yaml:"root"`
}

// Library is where the contract half of a design lands: the project root
// its manifest's `output.types` and event targets resolve against, and
// the Go import prefix that root carries.
type Library struct {
	Root    string
	Package string
}

// IsProjection reports whether this manifest generates from a design
// source it does not contain.
func (c *Config) IsProjection() bool { return c.SourceDesign != "" }

// LibraryRoot returns the filesystem root the contract half resolves
// against: the design source's project root for a projection, this
// project's own root otherwise.
func (c *Config) LibraryRoot(projectRoot string) string {
	if c.Library.Root != "" {
		return c.Library.Root
	}
	return projectRoot
}

// LibraryPackage returns the Go import prefix the contract half is
// generated under - [Config.Package] for a project holding its own
// design.
func (c *Config) LibraryPackage() string {
	if c.Library.Package != "" {
		return c.Library.Package
	}
	return c.Package
}

// Output groups every generated-artefact destination. Directory paths
// (Types, Transport, Routes, Service) are appended with `/<service>`
// per service at codegen time. File paths (Main, Svccontext, OpenAPI) point
// at the exact file that will be written. All paths are relative to the
// **project root** (the parent of the design folder).
// Output kinds. A project generates both halves of the design by default;
// a contracts project generates only the half other projects import.
const (
	// KindApplication generates the contract library and the application
	// around it: handlers, logic stubs, the dependency container, main.
	KindApplication = "application"
	// KindContracts generates only what other projects import: payload
	// types, the event library and the documents. Several deployables
	// share one, each with its own application project.
	KindContracts = "contracts"
)

type Output struct {
	// Kind selects how much of the design this project generates.
	// Defaults to [KindApplication].
	Kind string `yaml:"kind"`
	// Services narrows the application half to the named services,
	// each written `<package>.<Service>`. Empty generates every service
	// the design declares. It selects what this deployable RUNS - its
	// handlers, logic stubs, routes, wiring and container members - and
	// never the contract half, which is the whole design's shared
	// vocabulary and must read the same from every deployable.
	Services   []string `yaml:"services"`
	Types      string   `yaml:"types"`
	Transport  string   `yaml:"transport"`
	Routes     string   `yaml:"routes"`
	Service    string   `yaml:"service"`
	Main       string   `yaml:"main"`
	Svccontext string   `yaml:"svccontext"`
	OpenAPI    string   `yaml:"openapi"`
	// Middleware is the scaffold-once output dir for middleware
	// implementation files. The corresponding type declarations live
	// next to svccontext.go (see GenerateProjectMiddlewares).
	Middleware string `yaml:"middleware"`
	// ConsumeMiddleware is the scaffold-once output dir for
	// `consume middleware Name` implementations. It is a peer of
	// Middleware rather than a folder inside it: the two wrap different
	// things - an http.Handler against a subscription handler - and share
	// no code. Defaults to `./internal/consume`.
	ConsumeMiddleware string `yaml:"consumeMiddleware"`
	// Wiring is the directory holding the generated wiring package: the
	// one `Register` call main.go makes, whose surface does not change
	// with the design. Defaults to `./internal/wiring`.
	Wiring string `yaml:"wiring"`
	// Config is the scaffold-once directory holding the runtime
	// configuration package (config.go + config.yaml +
	// example.config.yaml). main.go reads from `<Config>/config.yaml`
	// at boot. Defaults to `./config`.
	Config string `yaml:"config"`
	// FileCase selects the naming convention for GENERATED file and
	// directory names derived from DSL identifiers - the per-method
	// handler/service files and the per-service directory. `snake`
	// (default) yields `create_user.go` / `user_service/`, `kebab`
	// yields `create-user.go` / `user-service/`, `camel` yields
	// `createUser.go` / `userService/`. It affects on-disk names only:
	// URL routes stay kebab-case, and Go package names / identifiers
	// are unchanged.
	FileCase string `yaml:"fileCase"`
}

// RuntimeDisabled reports whether the project opted out of the generated
// runtime layer (main.go, config, svccontext) with `output.main: "-"`.
func (o Output) RuntimeDisabled() bool { return o.Main == "-" }

// Supported values for [Output.FileCase]. They name the case used for
// generated file and directory names (not URLs or Go identifiers).
const (
	FileCaseKebab = "kebab"
	FileCaseSnake = "snake"
	FileCaseCamel = "camel"
	// DefaultFileCase applies when the manifest leaves fileCase unset.
	DefaultFileCase = FileCaseSnake
)

// Events configures the event pipeline: Targets lists the languages the
// event artefacts are generated for, AsyncAPI names the projection file.
// A manifest omitting the block gets [DefaultEventTargets].
//
// Transport and codec are runtime wiring, chosen where the application
// starts up, and are not part of this.
type Events struct {
	Targets  []EventTarget `yaml:"targets"`
	AsyncAPI string        `yaml:"asyncapi"`
}

// EventTarget is one language the event artefacts are generated for.
// Out is the destination directory, relative to the project root; "-"
// skips the target without removing the row.
type EventTarget struct {
	Lang string `yaml:"lang"`
	Out  string `yaml:"out"`
	// Layout overrides where individual artefacts land. No target reads
	// one - the Go target places its artefacts through the project-wide
	// `output:` block - so any value is rejected rather than ignored.
	Layout map[string]any `yaml:"layout"`
}

// Language names accepted in [EventTarget.Lang].
const (
	LangGo = "go"
)

// SupportedLangs is the closed set of languages a target may name. The
// generator's target catalogue is checked against it, so a language
// listed here without a generator - or the reverse - fails a test rather
// than silently producing nothing.
var SupportedLangs = []string{LangGo}

// RemovedLangs names languages craftgo used to generate, and what became
// of each. A manifest - or a `--target` - still naming one is told what
// happened rather than being handed the generic list of accepted names,
// which reads as a typo and sends the user looking for one.
var RemovedLangs = map[string]string{
	"typescript": "the typescript target was removed",
}

// DefaultEventTargets is the implicit target set for a manifest with no
// `events.targets` block. A contracts project is imported across modules,
// where Go forbids an `internal/` path, so its default lands outside.
func DefaultEventTargets(kind string) []EventTarget {
	if kind == KindContracts {
		return []EventTarget{{Lang: LangGo, Out: "./gen/events"}}
	}
	return []EventTarget{{Lang: LangGo, Out: "./internal/events"}}
}

// Enabled reports whether the target should be generated. `-` skips it
// without removing the row.
func (t EventTarget) Enabled() bool { return t.Out != "-" && t.Out != "" }

// TargetFor returns the configured target for lang.
func (e Events) TargetFor(lang string) (EventTarget, bool) {
	for _, t := range e.Targets {
		if t.Lang == lang {
			return t, true
		}
	}
	return EventTarget{}, false
}

// OpenAPI carries metadata that surfaces in the generated specification's
// info / servers blocks. BasePath is also used by the runtime to compute the
// final route string for each method. SecuritySchemes is a name → scheme
// map that powers the `@security(name)` cross-check: any DSL reference
// must resolve to a key here when the map is non-empty.
type OpenAPI struct {
	Title           string                    `yaml:"title"`
	Version         string                    `yaml:"version"`
	Description     string                    `yaml:"description"`
	BasePath        string                    `yaml:"basePath"`
	SecuritySchemes map[string]SecurityScheme `yaml:"securitySchemes"`
}

// SecurityScheme is the project-side projection of an OpenAPI 3.1
// security scheme object. Only the fields the codegen needs to
// validate references and emit OpenAPI components are modelled.
type SecurityScheme struct {
	// Type is the OpenAPI 3.1 scheme type: "http", "apiKey", "oauth2",
	// "openIdConnect", or "mutualTLS". Required.
	Type string `yaml:"type"`
	// Scheme is the HTTP authentication scheme name (`bearer`, `basic`).
	// Used only when Type == "http".
	Scheme string `yaml:"scheme,omitempty"`
	// BearerFormat hints at the bearer token shape (e.g. "JWT").
	BearerFormat string `yaml:"bearerFormat,omitempty"`
	// In is the apiKey location: "header", "query", or "cookie".
	In string `yaml:"in,omitempty"`
	// Name is the apiKey header / query / cookie name.
	Name string `yaml:"name,omitempty"`
	// OpenIDConnectURL is the discovery URL for openIdConnect.
	OpenIDConnectURL string `yaml:"openIdConnectUrl,omitempty"`
	// Flows configures the OAuth2 flows. Required (with at least one flow)
	// when Type == "oauth2" - the OpenAPI spec mandates a `flows` object, and
	// omitting it produces an invalid document that downstream client
	// generators (e.g. @hey-api/openapi-ts) reject.
	Flows *OAuthFlows `yaml:"flows,omitempty"`
}

// OAuthFlows mirrors the OpenAPI `oauthFlows` object: the four standard grant
// flows, each optional but at least one required for a valid oauth2 scheme.
type OAuthFlows struct {
	Implicit          *OAuthFlow `yaml:"implicit,omitempty"`
	Password          *OAuthFlow `yaml:"password,omitempty"`
	ClientCredentials *OAuthFlow `yaml:"clientCredentials,omitempty"`
	AuthorizationCode *OAuthFlow `yaml:"authorizationCode,omitempty"`
}

// OAuthFlow mirrors the OpenAPI `oauthFlow` object for one grant type.
type OAuthFlow struct {
	AuthorizationURL string            `yaml:"authorizationUrl,omitempty"`
	TokenURL         string            `yaml:"tokenUrl,omitempty"`
	RefreshURL       string            `yaml:"refreshUrl,omitempty"`
	Scopes           map[string]string `yaml:"scopes,omitempty"`
}

// flowList returns the non-nil flows in deterministic order, for emission and
// the "at least one flow" validation.
func (f *OAuthFlows) flowList() []*OAuthFlow {
	if f == nil {
		return nil
	}
	var out []*OAuthFlow
	for _, fl := range []*OAuthFlow{f.Implicit, f.Password, f.ClientCredentials, f.AuthorizationCode} {
		if fl != nil {
			out = append(out, fl)
		}
	}
	return out
}

// HasFlow reports whether at least one OAuth2 flow is configured.
func (f *OAuthFlows) HasFlow() bool { return len(f.flowList()) > 0 }

// Filename is the canonical project manifest file name. Find walks parent
// directories looking for it, optionally peeking into a child `design/`
// directory at each level.
const Filename = "craftgo.design.yaml"

// DesignFileExtensions are the extensions a craftgo source file may carry.
// `.craftgo` is canonical; `.cg` is the short alias. Both are accepted
// everywhere design sources are discovered - `craftgo gen`, `craftgo fmt`,
// and the language server's project walk and file watcher - and a single
// project may freely mix the two.
var DesignFileExtensions = []string{".craftgo", ".cg"}

// IsDesignFile reports whether path names a craftgo source file, matching its
// extension against [DesignFileExtensions]. path may be a full path or a bare
// file name.
func IsDesignFile(path string) bool {
	return slices.Contains(DesignFileExtensions, filepath.Ext(path))
}

// Find walks upward from `start` until it locates a [Filename]. At every
// candidate directory two strategies are tried, in order: the directory
// itself, then any direct subdirectory containing the manifest - so
// users can invoke `craftgo gen` from either the design folder or its
// parent regardless of what the design folder is named (`design`,
// `contracts`, `apis/v1`, ...). When more than one direct subdir
// holds a manifest the function bails out with an unambiguous error
// rather than silently picking one - the caller should pass an
// explicit folder via [FindAt].
//
// On success it returns the loaded [*Config], the absolute path of the
// project root (the parent of the design folder), and the
// absolute path of the design folder itself. Every `.craftgo` source
// file lives in the design folder or its descendants.
func Find(start string) (*Config, string, string, error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return nil, "", "", err
	}
	dir := abs
	for {
		// Direct hit - manifest sits in dir.
		if path := filepath.Join(dir, Filename); fileExists(path) {
			cfg, err := Load(path)
			if err != nil {
				return nil, "", "", err
			}
			return cfg, filepath.Dir(dir), dir, nil
		}
		// Probe direct subdirs. Allows the project to use any name
		// for the design folder (`design`, `contracts`, `dsl`, ...).
		// Multiple matches → ambiguous; require explicit -f flag.
		if matches, err := probeDesignSubdirs(dir); err == nil {
			switch len(matches) {
			case 1:
				designDir := matches[0]
				cfg, err := Load(filepath.Join(designDir, Filename))
				if err != nil {
					return nil, "", "", err
				}
				return cfg, dir, designDir, nil
			case 0:
				// keep walking up
			default:
				return nil, "", "", fmt.Errorf("multiple craftgo.design.yaml found under %s (%d matches); pass -f to disambiguate", dir, len(matches))
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, "", "", errors.New("craftgo.design.yaml not found in any parent directory or direct subdirectory")
		}
		dir = parent
	}
}

// FindAt loads the manifest at `<designFolder>/craftgo.design.yaml` and
// returns it alongside the resolved project root. When `projectRoot`
// is empty the parent of `designFolder` is used, the same root [Find]
// resolves by walking up; pass an explicit value when the design folder
// lives outside the project tree - the monorepo case where contracts/
// and services/ are siblings.
//
// All paths in the returned tuple are absolute.
func FindAt(designFolder, projectRoot string) (*Config, string, string, error) {
	absDesign, err := filepath.Abs(designFolder)
	if err != nil {
		return nil, "", "", err
	}
	manifest := filepath.Join(absDesign, Filename)
	if !fileExists(manifest) {
		return nil, "", "", fmt.Errorf("craftgo.design.yaml not found in %s", absDesign)
	}
	cfg, err := Load(manifest)
	if err != nil {
		return nil, "", "", err
	}
	absRoot := projectRoot
	if absRoot == "" {
		absRoot = filepath.Dir(absDesign)
	} else {
		absRoot, err = filepath.Abs(absRoot)
		if err != nil {
			return nil, "", "", err
		}
	}
	return cfg, absRoot, absDesign, nil
}

// probeDesignSubdirs returns every direct subdir of `dir` that
// contains a [Filename]. Hidden dirs (starting with `.`) and common
// vendor/output dirs are skipped to avoid stumbling into generated
// `internal/` trees that happen to nest a manifest from a sibling
// project.
func probeDesignSubdirs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "" || name[0] == '.' || name == "node_modules" || name == "vendor" {
			continue
		}
		path := filepath.Join(dir, name, Filename)
		if fileExists(path) {
			out = append(out, filepath.Join(dir, name))
		}
	}
	return out, nil
}

// fileExists is a small wrapper around os.Stat that ignores its error,
// returning true only when the path resolves to a regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// Load parses the manifest at `path`, validates required fields, applies
// defaults to optional ones, and returns the resulting [*Config].
//
// A manifest naming a design source (`design.from`) is a projection: the
// source manifest is loaded first and supplies the contract half, so the
// deployables of one design cannot drift apart on where their shared
// vocabulary lives. See [Config.resolveDesignSource].
func Load(path string) (*Config, error) { return load(path, nil) }

// load is [Load] carrying the manifests already being loaded, so a
// `design.from` naming its way back is reported instead of recursing
// until the stack runs out.
func load(path string, loading []string) (*Config, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if slices.Contains(loading, abs) {
		return nil, fmt.Errorf("design.from forms a cycle: %s", strings.Join(append(loading, abs), " -> "))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.ManifestDir = filepath.Dir(abs)
	if err := cfg.resolveDesignSource(cfg.ManifestDir, append(loading, abs)); err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	cfg.applyDefaults()
	// Collisions are checked against final values: an explicit key can
	// land on another key's default, which is the shape that builds today
	// and breaks on the next design edit.
	if err := cfg.checkOutputUsable(); err != nil {
		return nil, err
	}
	if err := cfg.checkOutputCollisions(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// validate checks required manifest fields. Every required field has
// either a default or is populated post-Load (Package via go.mod), so
// there is nothing to reject; it stays as a hook for future required
// keys without re-wiring callers.
func (c *Config) validate() error {
	for _, out := range []struct{ key, val string }{
		{"output.types", c.Output.Types},
		{"output.transport", c.Output.Transport},
		{"output.routes", c.Output.Routes},
		{"output.service", c.Output.Service},
		{"output.main", c.Output.Main},
		{"output.svccontext", c.Output.Svccontext},
		{"output.openapi", c.Output.OpenAPI},
		{"output.middleware", c.Output.Middleware},
		{"output.consumeMiddleware", c.Output.ConsumeMiddleware},
		{"output.config", c.Output.Config},
		{"output.wiring", c.Output.Wiring},
		{"events.asyncapi", c.Events.AsyncAPI},
	} {
		if err := checkWithinProject(out.key, out.val); err != nil {
			return err
		}
	}
	for _, t := range c.Events.Targets {
		key := "events.targets[" + t.Lang + "]"
		if err := checkWithinProject(key+".out", t.Out); err != nil {
			return err
		}
		if len(t.Layout) > 0 {
			return fmt.Errorf("%s.layout: no target reads a `layout:` - the go target places its artefacts through the `output:` block", key)
		}
	}
	switch c.Output.FileCase {
	case "", FileCaseKebab, FileCaseSnake, FileCaseCamel:
	default:
		return fmt.Errorf("output.fileCase %q is not supported - use %q, %q, or %q",
			c.Output.FileCase, FileCaseKebab, FileCaseSnake, FileCaseCamel)
	}
	if err := c.checkServiceSelection(); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, t := range c.Events.Targets {
		if t.Lang == "" {
			return errors.New("events.targets entry is missing `lang`")
		}
		if gone, ok := RemovedLangs[t.Lang]; ok {
			return fmt.Errorf("events.targets lang %q: %s - drop the row; the DSL and the Go output are unaffected", t.Lang, gone)
		}
		if !slices.Contains(SupportedLangs, t.Lang) {
			return fmt.Errorf("events.targets lang %q is not supported - use %s",
				t.Lang, quotedList(SupportedLangs))
		}
		if seen[t.Lang] {
			return fmt.Errorf("events.targets lists %q twice", t.Lang)
		}
		seen[t.Lang] = true
		if t.Out == "" {
			return fmt.Errorf("events.targets entry %q has no `out` - set a directory, or %q to skip the target", t.Lang, "-")
		}
	}
	return nil
}

// applyDefaults fills in any blank optional path with the framework's
// recommended location. Mirrors the README "Configuration" section so
// projects can run with an empty manifest and inherit every default.
func (c *Config) applyDefaults() {
	if c.Output.Kind == "" {
		c.Output.Kind = KindApplication
	}
	if c.Output.Types == "" {
		// Same reason as [DefaultEventTargets]: an importer needs the
		// payload type to build a message, and cannot reach `internal/`
		// across modules.
		c.Output.Types = "./internal/types"
		if c.Output.ContractsOnly() {
			c.Output.Types = "./gen/types"
		}
	}
	if c.Output.Transport == "" {
		c.Output.Transport = "./internal/transport"
	}
	if c.Output.Routes == "" {
		c.Output.Routes = "./internal/routes"
	}
	if c.Output.Service == "" {
		c.Output.Service = "./internal/service"
	}
	if c.Output.Main == "" {
		c.Output.Main = "./main.go"
	}
	if c.Output.Svccontext == "" {
		c.Output.Svccontext = "./svccontext/svccontext.go"
	}
	if c.Output.OpenAPI == "" {
		c.Output.OpenAPI = "./docs/openapi.yaml"
	}
	if c.Output.Middleware == "" {
		c.Output.Middleware = "./internal/middleware"
	}
	if c.Output.ConsumeMiddleware == "" {
		c.Output.ConsumeMiddleware = "./internal/consume"
	}
	if c.Output.Config == "" {
		c.Output.Config = "./config"
	}
	if c.Output.Wiring == "" {
		c.Output.Wiring = "./internal/wiring"
	}
	if c.Output.FileCase == "" {
		c.Output.FileCase = DefaultFileCase
	}
	if len(c.Events.Targets) == 0 {
		c.Events.Targets = DefaultEventTargets(c.Output.Kind)
	}
	if c.Events.AsyncAPI == "" {
		c.Events.AsyncAPI = "./docs/asyncapi.yaml"
	}
}

// ResolveModulePath walks upward from `projectRoot` looking for a
// `go.mod`, parses its `module ...` line, and appends the relative
// path from go.mod's directory to projectRoot. The result is the
// Go import-path prefix every generated file uses for its imports.
//
// Examples:
//
//	go.mod at repo/, module "github.com/foo/bar", projectRoot=repo/
//	  → "github.com/foo/bar"
//	go.mod at repo/, module "github.com/foo/bar", projectRoot=repo/services/api
//	  → "github.com/foo/bar/services/api"
//
// The walk-up handles both the simple single-module project (go.mod
// at project root) and the monorepo with one shared go.mod at the
// repo root and project root inside a sub-tree. Errors when no
// go.mod is found anywhere upward - gen needs the canonical module
// path to emit imports the Go compiler can resolve.
func ResolveModulePath(projectRoot string) (string, error) {
	abs, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", err
	}
	dir := abs
	for {
		modPath := filepath.Join(dir, "go.mod")
		if data, err := os.ReadFile(modPath); err == nil {
			modLine := parseModuleLine(data)
			if modLine == "" {
				return "", fmt.Errorf("malformed go.mod at %s: missing `module` line", modPath)
			}
			rel, relErr := filepath.Rel(dir, abs)
			if relErr != nil {
				return "", relErr
			}
			rel = filepath.ToSlash(rel)
			if rel == "." {
				return modLine, nil
			}
			return modLine + "/" + rel, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod found at %s or any parent directory; run `go mod init <module>` first", projectRoot)
		}
		dir = parent
	}
}

// parseModuleLine scans a go.mod body for the first `module <path>`
// declaration and returns the path. Avoids pulling in
// `golang.org/x/mod/modfile` for a single line of parsing - go.mod
// syntax for the module clause is fixed and trivial to scan.
func parseModuleLine(data []byte) string {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "module") {
			continue
		}
		// Either `module <path>` or `module "<path>"`. Support both
		// forms - go.mod accepts quoted paths for unusual chars.
		rest := strings.TrimSpace(strings.TrimPrefix(line, "module"))
		rest = strings.TrimSuffix(strings.TrimPrefix(rest, `"`), `"`)
		if rest != "" {
			return rest
		}
	}
	return ""
}

// quotedList renders names as `"a"`, `"a" or "b"`, `"a", "b" or "c"` for
// an error message that lists the accepted values.
func quotedList(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = strconv.Quote(n)
	}
	switch len(quoted) {
	case 0:
		return ""
	case 1:
		return quoted[0]
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
}

// checkWithinProject rejects an output path that escapes the project
// root. Generated Go files import each other by `<module path>/<output
// dir>`, and a path outside the module has no such spelling - craftgo
// would write the files and the import would only fail later, at
// `go build`, as `invalid path element ".."`.
// checkOutputUsable rejects `-` on a key that has no disabled mode. Only
// main.go, the documents and the event targets can be turned off; the rest
// name a package other generated code imports.
// ContractsOnly reports whether this project generates only the half other
// projects import.
func (o Output) ContractsOnly() bool { return o.Kind == KindContracts }

func (c *Config) checkOutputUsable() error {
	switch c.Output.Kind {
	case KindApplication, KindContracts:
	default:
		return fmt.Errorf("output.kind %q is not one of %q, %q", c.Output.Kind, KindApplication, KindContracts)
	}
	for _, out := range []struct{ key, val string }{
		{"output.types", c.Output.Types},
		{"output.transport", c.Output.Transport},
		{"output.routes", c.Output.Routes},
		{"output.wiring", c.Output.Wiring},
		{"output.service", c.Output.Service},
		{"output.svccontext", c.Output.Svccontext},
		{"output.middleware", c.Output.Middleware},
		{"output.consumeMiddleware", c.Output.ConsumeMiddleware},
		{"output.config", c.Output.Config},
	} {
		if out.val == "-" {
			return fmt.Errorf(`%s cannot be "-" - other generated code imports this package, so there is nothing to disable; "-" is for output.main, output.openapi and the event targets`, out.key)
		}
	}
	return nil
}

// checkOutputCollisions rejects two output keys resolving to one directory.
// Each generated root file has a package clause fixed by its role, so a
// shared directory holds two of them and never compiles.
//
// `output.main` contributes its directory - the module root by default,
// where any package collides with `package main`.
func (c *Config) checkOutputCollisions() error {
	dirs := []struct{ key, dir string }{
		{"output.types", outputDir(c.typesDirForCollision())},
		{"output.transport", outputDir(c.Output.Transport)},
		{"output.routes", outputDir(c.Output.Routes)},
		{"output.wiring", outputDir(c.Output.Wiring)},
		{"output.service", outputDir(c.Output.Service)},
		{"output.middleware", outputDir(c.Output.Middleware)},
		{"output.consumeMiddleware", outputDir(c.Output.ConsumeMiddleware)},
		{"output.config", outputDir(c.Output.Config)},
		{"output.svccontext", outputFileDir(c.Output.Svccontext)},
		{"output.main", outputFileDir(c.Output.Main)},
	}
	seen := map[string]string{}
	for _, d := range dirs {
		if d.dir == "" {
			continue
		}
		if first, dup := seen[d.dir]; dup {
			return fmt.Errorf("%s and %s both write to %q - each generated package needs its own directory, or the two package clauses land in one and nothing compiles", first, d.key, d.dir)
		}
		seen[d.dir] = d.key
	}
	return nil
}

// typesDirForCollision returns the types output as far as the collision
// check is concerned. A projection's types land under the design
// source's root, so they share no directory with this project's own
// output whatever the two paths spell.
func (c *Config) typesDirForCollision() string {
	if c.IsProjection() {
		return ""
	}
	return c.Output.Types
}

// outputDir normalises an output path for comparison. A disabled key ("-")
// and an unset one contribute nothing.
func outputDir(val string) string {
	if val == "" || val == "-" {
		return ""
	}
	return path.Clean(toSlash(val))
}

// outputFileDir is [outputDir] for a key naming a FILE rather than a
// directory (`output.main`, `output.svccontext`): the package it joins is
// the directory holding it.
func outputFileDir(val string) string {
	if val == "" || val == "-" {
		return ""
	}
	return path.Clean(path.Dir(toSlash(val)))
}

// toSlash rewrites a Windows-style path so comparisons and `path` helpers
// see the separator they expect.
func toSlash(val string) string { return strings.ReplaceAll(val, "\\", "/") }

func checkWithinProject(key, val string) error {
	if val == "" || val == "-" {
		return nil
	}
	clean := path.Clean(strings.ReplaceAll(val, "\\", "/"))
	if clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
		return fmt.Errorf("%s %q must stay inside the project - generated code is imported as `<module>/<path>`, which cannot name a directory outside the module", key, val)
	}
	return nil
}
