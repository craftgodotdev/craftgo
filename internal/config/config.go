// Package config loads and validates `craftgo.design.yaml`, the project
// manifest. The manifest sits in the design folder; the folder's parent is
// the project root that `output:` paths are relative to.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/walk"
)

// Config is the decoded `craftgo.design.yaml`.
type Config struct {
	Output  Output  `yaml:"output"`
	OpenAPI OpenAPI `yaml:"openapi"`
	Events  Events  `yaml:"events"`
	Proto   Proto   `yaml:"proto"`

	// Package is the Go import path of the project root. Load leaves it
	// empty; gen sets it from [ResolveModulePath].
	Package string `yaml:"-"`
	// Warnings names each manifest key Load ignored because no field declares
	// it, with what replaced a removed key.
	Warnings []string `yaml:"-"`
}

// Values of [Output.Kind].
const (
	// KindApplication generates the contract library and the application
	// around it: handlers, logic stubs, the dependency container, main.
	KindApplication = "application"
	// KindContracts generates only what other projects import: payload
	// types, pb code, the event library and the documents.
	KindContracts = "contracts"
)

// Output holds the `output:` keys. Paths are relative to the project root;
// Main, Svccontext and OpenAPI name files, the other paths directories.
type Output struct {
	// Kind is [KindApplication] or [KindContracts].
	Kind       string `yaml:"kind"`
	Types      string `yaml:"types"`
	Transport  string `yaml:"transport"`
	Routes     string `yaml:"routes"`
	Service    string `yaml:"service"`
	Main       string `yaml:"main"`
	Svccontext string `yaml:"svccontext"`
	OpenAPI    string `yaml:"openapi"`
	// Middleware is the scaffold-once directory of the middleware implementations.
	Middleware string `yaml:"middleware"`
	// Wiring is the directory of the generated package whose Register main.go calls.
	Wiring string `yaml:"wiring"`
	// Config is the scaffold-once directory of the runtime configuration package.
	Config string `yaml:"config"`
	// PB is the directory the protobuf plugins write into. "-" runs no
	// plugin, and every design proto must then carry `option go_package`.
	PB string `yaml:"pb"`
	// GRPC is the directory of the generated gRPC server layer, one package
	// per proto service.
	GRPC string `yaml:"grpc"`
	// FileCase is the case of generated file and directory names derived
	// from DSL identifiers; routes and Go identifiers keep their own case.
	FileCase string `yaml:"fileCase"`
}

// Disabled is the value that turns off an output key or event target that
// accepts it.
const Disabled = "-"

// RuntimeDisabled reports whether the project opted out of the generated
// runtime layer (main.go, config, svccontext) with `output.main: "-"`.
func (o Output) RuntimeDisabled() bool { return o.Main == Disabled }

// PBDisabled reports whether the project opted out of running the
// protobuf plugins with `output.pb: "-"`.
func (o Output) PBDisabled() bool { return o.PB == Disabled }

// OpenAPIDisabled reports whether the project writes no OpenAPI document:
// `output.openapi` is "-" or empty.
func (o Output) OpenAPIDisabled() bool { return o.OpenAPI == "" || o.OpenAPI == Disabled }

// outputKey is one path key of the `output:` block.
type outputKey struct {
	name  string
	field func(*Output) *string
	// def is the default; contractsDef, when set, replaces it in a contracts
	// project, whose packages other modules import from outside internal/.
	def, contractsDef string
	// file marks a key naming a file; the directory holding it is what can
	// collide with another key's.
	file bool
	// disableable accepts [Disabled].
	disableable bool
	// document marks a key whose file holds no Go code.
	document bool
}

// outputKeys lists every path key of the `output:` block, in the order the
// checks report them.
var outputKeys = []outputKey{
	{name: "types", field: func(o *Output) *string { return &o.Types }, def: "./internal/types", contractsDef: "./gen/types"},
	{name: "transport", field: func(o *Output) *string { return &o.Transport }, def: "./internal/transport"},
	{name: "routes", field: func(o *Output) *string { return &o.Routes }, def: "./internal/routes"},
	{name: "wiring", field: func(o *Output) *string { return &o.Wiring }, def: "./internal/wiring"},
	{name: "service", field: func(o *Output) *string { return &o.Service }, def: "./internal/service"},
	{name: "middleware", field: func(o *Output) *string { return &o.Middleware }, def: "./internal/middleware"},
	{name: "config", field: func(o *Output) *string { return &o.Config }, def: "./config"},
	{name: "pb", field: func(o *Output) *string { return &o.PB }, def: "./internal/pb", contractsDef: "./gen/pb", disableable: true},
	{name: "grpc", field: func(o *Output) *string { return &o.GRPC }, def: "./internal/grpc"},
	{name: "svccontext", field: func(o *Output) *string { return &o.Svccontext }, def: "./svccontext/svccontext.go", file: true},
	{name: "main", field: func(o *Output) *string { return &o.Main }, def: "./main.go", file: true, disableable: true},
	{name: "openapi", field: func(o *Output) *string { return &o.OpenAPI }, def: "./docs/openapi.yaml", file: true, disableable: true, document: true},
}

// key returns the manifest spelling of k, `output.<name>`.
func (k outputKey) key() string { return "output." + k.name }

// Proto configures how the design folder's `.proto` files compile. The design
// folder is the first import root.
type Proto struct {
	// Includes lists extra import roots, relative to the project root, whose
	// protos are imported but not generated, so each needs `option go_package`.
	Includes []string `yaml:"includes"`
	// Plugins names the plugin executables. Empty runs each one through
	// `go tool <name>`, pinned by the `tool` directives in go.mod.
	Plugins Plugins `yaml:"plugins"`
}

// Plugins names the two protobuf plugins. A value is a command: a bare
// name is looked up on PATH, a path is run as given.
type Plugins struct {
	Go     string `yaml:"go"`
	GoGRPC string `yaml:"goGrpc"`
}

// Events lists the languages the event artefacts are generated for; with none,
// Load sets the go target (`./internal/events`, `./gen/events` for contracts).
type Events struct {
	Targets []EventTarget `yaml:"targets"`
}

// EventTarget is one language the event artefacts are generated for.
// Out is the destination directory, relative to the project root; "-"
// skips the target without removing the row.
type EventTarget struct {
	Lang string `yaml:"lang"`
	Out  string `yaml:"out"`
}

// Language names accepted in [EventTarget.Lang].
const (
	LangGo = "go"
)

// SupportedLangs lists the languages an event target may name.
var SupportedLangs = []string{LangGo}

// defaultEventTargets returns the targets of a manifest with none. A contracts
// project's default is outside `internal/`, which other modules cannot import.
func defaultEventTargets(kind string) []EventTarget {
	if kind == KindContracts {
		return []EventTarget{{Lang: LangGo, Out: "./gen/events"}}
	}
	return []EventTarget{{Lang: LangGo, Out: "./internal/events"}}
}

// DefaultEventDirs lists the directories the Go event target takes by default,
// for one project kind or the other.
func DefaultEventDirs() []string {
	return []string{defaultEventTargets(KindApplication)[0].Out, defaultEventTargets(KindContracts)[0].Out}
}

// Enabled reports whether the target is generated; "-" skips it.
func (t EventTarget) Enabled() bool { return t.Out != Disabled && t.Out != "" }

// key returns the manifest spelling of t's destination, `events.targets[<lang>].out`.
func (t EventTarget) key() string { return "events.targets[" + t.Lang + "].out" }

// TargetFor returns the configured target for lang.
func (e Events) TargetFor(lang string) (EventTarget, bool) {
	for _, t := range e.Targets {
		if t.Lang == lang {
			return t, true
		}
	}
	return EventTarget{}, false
}

// OpenAPI holds the `openapi:` keys. BasePath prefixes every generated route
// and is the document's server URL; when SecuritySchemes is non-empty, every
// `@security(name)` must name one of its keys.
type OpenAPI struct {
	Title           string                    `yaml:"title"`
	Version         string                    `yaml:"version"`
	Description     string                    `yaml:"description"`
	BasePath        string                    `yaml:"basePath"`
	SecuritySchemes map[string]SecurityScheme `yaml:"securitySchemes"`
}

// SecurityScheme is the part of an OpenAPI 3.1 security scheme object that
// craftgo models.
type SecurityScheme struct {
	// Type is "http", "apiKey", "oauth2", "openIdConnect" or "mutualTLS".
	Type string `yaml:"type"`
	// Scheme is the HTTP authentication scheme (`bearer`, `basic`) of an "http" type.
	Scheme string `yaml:"scheme,omitempty"`
	// BearerFormat hints at the bearer token shape (e.g. "JWT").
	BearerFormat string `yaml:"bearerFormat,omitempty"`
	// In is the apiKey location: "header", "query", or "cookie".
	In string `yaml:"in,omitempty"`
	// Name is the apiKey header / query / cookie name.
	Name string `yaml:"name,omitempty"`
	// OpenIDConnectURL is the discovery URL for openIdConnect.
	OpenIDConnectURL string `yaml:"openIdConnectUrl,omitempty"`
	// Flows configures the OAuth2 flows; OpenAPI requires at least one for an
	// "oauth2" type.
	Flows *OAuthFlows `yaml:"flows,omitempty"`
}

// OAuthFlows is the OpenAPI `oauthFlows` object, one optional flow per grant type.
type OAuthFlows struct {
	Implicit          *OAuthFlow `yaml:"implicit,omitempty"`
	Password          *OAuthFlow `yaml:"password,omitempty"`
	ClientCredentials *OAuthFlow `yaml:"clientCredentials,omitempty"`
	AuthorizationCode *OAuthFlow `yaml:"authorizationCode,omitempty"`
}

// OAuthFlow is the OpenAPI `oauthFlow` object of one grant type.
type OAuthFlow struct {
	AuthorizationURL string            `yaml:"authorizationUrl,omitempty"`
	TokenURL         string            `yaml:"tokenUrl,omitempty"`
	RefreshURL       string            `yaml:"refreshUrl,omitempty"`
	Scopes           map[string]string `yaml:"scopes,omitempty"`
}

// flowList returns the configured flows in a fixed order.
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

// Filename is the project manifest's file name.
const Filename = "craftgo.design.yaml"

// DesignFileExtensions are the extensions of a design source file; `.cg` is
// short for `.craftgo`.
var DesignFileExtensions = []string{".craftgo", ".cg"}

// IsDesignFile reports whether path, a full path or a bare file name, has one
// of the [DesignFileExtensions].
func IsDesignFile(path string) bool {
	return slices.Contains(DesignFileExtensions, filepath.Ext(path))
}

// Find walks up from start until a directory, or one of its direct
// subdirectories, holds a [Filename]; several such subdirectories are an error.
// It probes the subdirectories a design walk enters, links to directories
// included, and stops at the first directory that holds go.mod or is a
// version-control root, so a design of an enclosing module or repository is
// never picked up. It returns the loaded config and the absolute project root
// and design folder.
func Find(start string) (*Config, string, string, error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return nil, "", "", err
	}
	dir := abs
	for {
		path := filepath.Join(dir, Filename)
		found, err := manifestAt(path)
		if err != nil {
			return nil, "", "", err
		}
		if found {
			cfg, err := Load(path)
			if err != nil {
				return nil, "", "", err
			}
			return cfg, filepath.Dir(dir), dir, nil
		}
		matches, err := probeDesignSubdirs(dir)
		if err != nil {
			return nil, "", "", err
		}
		switch len(matches) {
		case 0:
		case 1:
			designDir := matches[0]
			cfg, err := Load(filepath.Join(designDir, Filename))
			if err != nil {
				return nil, "", "", err
			}
			return cfg, dir, designDir, nil
		default:
			return nil, "", "", fmt.Errorf("multiple craftgo.design.yaml found under %s (%d matches); pass -f to disambiguate", dir, len(matches))
		}
		if bound := walkUpBound(dir); bound != "" {
			where := abs + " or a directory above it up to " + dir
			if dir == abs {
				where = abs
			}
			return nil, "", "", fmt.Errorf("craftgo.design.yaml not found in %s, which %s, nor in a direct subdirectory - pass -f to name the design folder", where, bound)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, "", "", errors.New("craftgo.design.yaml not found in any parent directory or direct subdirectory")
		}
		dir = parent
	}
}

// walkUpBound says why [Find] stops at dir - it holds go.mod, or is the root
// of a repository - or is "" when the walk goes on.
func walkUpBound(dir string) string {
	if info, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil && !info.IsDir() {
		return "holds go.mod"
	}
	for _, vcs := range []string{".git", ".hg", ".svn"} {
		if _, err := os.Stat(filepath.Join(dir, vcs)); err == nil {
			return "is the root of a repository (" + vcs + ")"
		}
	}
	return ""
}

// FindAt loads the manifest in designFolder and returns it with the absolute
// project root, the design folder's parent, and design folder.
func FindAt(designFolder string) (*Config, string, string, error) {
	absDesign, err := filepath.Abs(designFolder)
	if err != nil {
		return nil, "", "", err
	}
	manifest := filepath.Join(absDesign, Filename)
	found, err := manifestAt(manifest)
	if err != nil {
		return nil, "", "", err
	}
	if !found {
		return nil, "", "", fmt.Errorf("craftgo.design.yaml not found in %s", absDesign)
	}
	cfg, err := Load(manifest)
	if err != nil {
		return nil, "", "", err
	}
	return cfg, filepath.Dir(absDesign), absDesign, nil
}

// probeDesignSubdirs returns the direct subdirectories of dir, or links to
// directories, that hold a [Filename], skipping those [walk.SkipDesignDir]
// names. A dir it cannot read holds none.
func probeDesignSubdirs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	var out []string
	for _, e := range entries {
		sub := filepath.Join(dir, e.Name())
		if walk.SkipDesignDir(e.Name()) || !isDir(sub) {
			continue
		}
		found, err := manifestAt(filepath.Join(sub, Filename))
		if err != nil {
			return nil, err
		}
		if found {
			out = append(out, sub)
		}
	}
	return out, nil
}

// isDir reports whether path is a directory or a link to one.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// manifestAt reports whether a manifest file is at path; a symbolic link
// there that leads nowhere is an error, not an absent manifest.
func manifestAt(path string) (bool, error) {
	info, err := os.Stat(path)
	if err == nil {
		return !info.IsDir(), nil
	}
	if link, lerr := os.Lstat(path); lerr == nil && link.Mode()&os.ModeSymlink != 0 && errors.Is(err, fs.ErrNotExist) {
		target, _ := os.Readlink(path)
		return false, fmt.Errorf("%s is a symbolic link to %s, which does not exist", path, target)
	}
	return false, nil
}

// Load reads, validates and defaults the manifest at path. A removed key is an
// error; any other key no field declares is ignored and named in [Config.Warnings].
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	var cfg Config
	if err := doc.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	for _, key := range undeclaredKeys(&doc, reflect.TypeFor[Config](), "") {
		if note, ok := removedKeys[listIndex.ReplaceAllString(key, "[]")]; ok {
			return nil, fmt.Errorf("%s is no longer a manifest key - %s; drop it", key, note)
		}
		cfg.Warnings = append(cfg.Warnings, key+" is not a manifest key and is ignored")
	}
	if laterDocument(dec) {
		cfg.Warnings = append(cfg.Warnings, "the YAML document after `---` is ignored - the manifest is its first document")
	}
	cfg.normalizePaths()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	cfg.applyDefaults()
	// Checked after defaults: an explicit key can land on another key's default.
	if err := cfg.checkOutputUsable(); err != nil {
		return nil, err
	}
	if err := cfg.checkOutputCollisions(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// laterDocument reports whether dec, past the manifest's first document, holds
// another with content, or one it cannot parse; a bare `---` or `...` at the
// end holds none.
func laterDocument(dec *yaml.Decoder) bool {
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return false
		}
		if err != nil {
			return true
		}
		if len(doc.Content) > 0 && doc.Content[0].Tag != "!!null" {
			return true
		}
	}
}

// normalizePaths spells every path the manifest names with forward slashes,
// whatever the OS, so the files gen writes, the import paths it derives and
// main.go's go:embed agree on a manifest written on Windows.
func (c *Config) normalizePaths() {
	for _, k := range outputKeys {
		p := k.field(&c.Output)
		*p = toSlash(*p)
	}
	for i := range c.Events.Targets {
		c.Events.Targets[i].Out = toSlash(c.Events.Targets[i].Out)
	}
	for i := range c.Proto.Includes {
		c.Proto.Includes[i] = toSlash(c.Proto.Includes[i])
	}
}

// removedKeys are the keys the manifest no longer reads, each with what took
// its place; a list index in a key reads `[]`.
var removedKeys = map[string]string{
	"design":                   "a manifest holds its own design folder - generate each deployable from the design beside it",
	"output.services":          "a project generates every service its design declares",
	"output.consumeMiddleware": "middleware is installed on the bus with bus.Use, or on one subscription through Subscription.Chain",
	"events.asyncapi":          "craftgo writes no asyncapi document",
	"events.targets[].layout":  "the go target places its artefacts through the `output:` block",
}

// listIndex matches the index of a list item in a key path.
var listIndex = regexp.MustCompile(`\[\d+\]`)

// undeclaredKeys returns, in document order, the path of every mapping key
// under node that t, the type node decodes into, declares no field for. A path
// joins keys with dots and writes a list item as `[i]`: `events.targets[0].out`.
func undeclaredKeys(node *yaml.Node, t reflect.Type, path string) []string {
	if node.Kind == yaml.AliasNode {
		node = node.Alias
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	var out []string
	switch {
	case node.Kind == yaml.DocumentNode:
		for _, n := range node.Content {
			out = append(out, undeclaredKeys(n, t, path)...)
		}
	case node.Kind == yaml.SequenceNode && t.Kind() == reflect.Slice:
		for i, n := range node.Content {
			out = append(out, undeclaredKeys(n, t.Elem(), fmt.Sprintf("%s[%d]", path, i))...)
		}
	case node.Kind == yaml.MappingNode && (t.Kind() == reflect.Struct || t.Kind() == reflect.Map):
		var fields map[string]reflect.Type
		if t.Kind() == reflect.Struct {
			fields = yamlFields(t)
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, val := node.Content[i], node.Content[i+1]
			if key.Tag == "!!merge" {
				// `<<: *anchor` or `<<: [*a, *b]` merges the anchored mappings in.
				merged := []*yaml.Node{val}
				if val.Kind == yaml.SequenceNode {
					merged = val.Content
				}
				for _, m := range merged {
					out = append(out, undeclaredKeys(m, t, path)...)
				}
				continue
			}
			sub := key.Value
			if path != "" {
				sub = path + "." + key.Value
			}
			switch {
			case t.Kind() == reflect.Map:
				out = append(out, undeclaredKeys(val, t.Elem(), sub)...)
			case fields[key.Value] != nil:
				out = append(out, undeclaredKeys(val, fields[key.Value], sub)...)
			default:
				out = append(out, sub)
			}
		}
	}
	return out
}

// yamlFields maps each key the struct t declares to its field's type, as
// yaml.v3 names them: the tag's name, else the lower-cased field name.
func yamlFields(t reflect.Type) map[string]reflect.Type {
	fields := map[string]reflect.Type{}
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if !f.IsExported() || name == "-" {
			continue
		}
		if name == "" {
			name = strings.ToLower(f.Name)
		}
		fields[name] = f.Type
	}
	return fields
}

// validate checks the manifest as written, before defaults apply.
func (c *Config) validate() error {
	for _, k := range outputKeys {
		if err := checkWithinProject(k.key(), *k.field(&c.Output)); err != nil {
			return err
		}
	}
	// An include root is only read, so it may sit outside the project.
	for i, inc := range c.Proto.Includes {
		if inc == "" || inc == Disabled {
			return fmt.Errorf("proto.includes[%d]: an include names a directory", i)
		}
	}
	for _, t := range c.Events.Targets {
		if err := checkWithinProject(t.key(), t.Out); err != nil {
			return err
		}
	}
	switch c.Output.FileCase {
	case "", idents.FileCaseKebab, idents.FileCaseSnake, idents.FileCaseCamel:
	default:
		return fmt.Errorf("output.fileCase %q is not supported - use %q, %q, or %q",
			c.Output.FileCase, idents.FileCaseKebab, idents.FileCaseSnake, idents.FileCaseCamel)
	}
	seen := map[string]bool{}
	for _, t := range c.Events.Targets {
		if t.Lang == "" {
			return errors.New("events.targets entry is missing `lang`")
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
			return fmt.Errorf("events.targets entry %q has no `out` - set a directory, or %q to skip the target", t.Lang, Disabled)
		}
	}
	return nil
}

// applyDefaults fills every unset key with its default.
func (c *Config) applyDefaults() {
	if c.Output.Kind == "" {
		c.Output.Kind = KindApplication
	}
	for _, k := range outputKeys {
		if val := k.field(&c.Output); *val == "" {
			*val = k.def
			if c.Output.ContractsOnly() && k.contractsDef != "" {
				*val = k.contractsDef
			}
		}
	}
	if c.Output.FileCase == "" {
		c.Output.FileCase = idents.DefaultFileCase
	}
	// `targets: []` asks for no target; only an absent list takes the default.
	if c.Events.Targets == nil {
		c.Events.Targets = defaultEventTargets(c.Output.Kind)
	}
}

// ResolveModulePath returns the Go import path of projectRoot: the module path
// of the nearest go.mod at or above it, joined with projectRoot's path below
// that go.mod.
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

// parseModuleLine returns the module path a go.mod declares, or "": the
// argument of its `module` directive, quoted or not, in the line or the block
// form, without the comments beside it.
func parseModuleLine(data []byte) string {
	inBlock := false
	for line := range strings.Lines(string(data)) {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(strings.NewReplacer("(", " ( ", ")", " ) ").Replace(line))
		switch {
		case len(fields) == 0:
		case inBlock:
			if fields[0] == ")" {
				inBlock = false
				continue
			}
			return unquoteModulePath(fields[0])
		case fields[0] != "module" || len(fields) == 1:
		case fields[1] == "(":
			inBlock = true
		default:
			return unquoteModulePath(fields[1])
		}
	}
	return ""
}

// unquoteModulePath returns a module path written quoted or backquoted
// without its quotes.
func unquoteModulePath(s string) string {
	if u, err := strconv.Unquote(s); err == nil {
		return u
	}
	return s
}

// quotedList renders names as `"a"`, `"a" or "b"`, `"a", "b" or "c"`.
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

// ContractsOnly reports whether this project generates only the half other
// projects import.
func (o Output) ContractsOnly() bool { return o.Kind == KindContracts }

// checkOutputUsable rejects an unknown output.kind and "-" on a key that
// cannot be disabled.
func (c *Config) checkOutputUsable() error {
	switch c.Output.Kind {
	case KindApplication, KindContracts:
	default:
		return fmt.Errorf("output.kind %q is not one of %q, %q", c.Output.Kind, KindApplication, KindContracts)
	}
	var disableable []string
	for _, k := range outputKeys {
		if k.disableable {
			disableable = append(disableable, k.key())
		}
	}
	slices.Sort(disableable)
	for _, k := range outputKeys {
		if !k.disableable && *k.field(&c.Output) == Disabled {
			return fmt.Errorf(`%s cannot be %q - other generated code imports this package, so there is nothing to disable; %q is for %s and the event targets`,
				k.key(), Disabled, Disabled, strings.Join(disableable, ", "))
		}
	}
	return nil
}

// checkOutputCollisions rejects two outputs holding Go code, output keys or event targets, that
// resolve to one directory; a key naming a file contributes the file's directory.
func (c *Config) checkOutputCollisions() error {
	seen := map[string]string{}
	claim := func(key, dir string) error {
		if dir == "" {
			return nil
		}
		if first, dup := seen[dir]; dup {
			return fmt.Errorf("%s and %s both write to %q - each generated package needs its own directory, or the two package clauses land in one and nothing compiles", first, key, dir)
		}
		seen[dir] = key
		return nil
	}
	for _, k := range outputKeys {
		if k.document {
			continue
		}
		if err := claim(k.key(), k.dir(*k.field(&c.Output))); err != nil {
			return err
		}
	}
	for _, t := range c.Events.Targets {
		if err := claim(t.key(), outputKey{}.dir(t.Out)); err != nil {
			return err
		}
	}
	return nil
}

// dir normalises val, k's value, to the directory it writes to; "" and
// [Disabled] yield "".
func (k outputKey) dir(val string) string {
	if val == "" || val == Disabled {
		return ""
	}
	val = toSlash(val)
	if k.file {
		val = path.Dir(val)
	}
	return path.Clean(val)
}

// toSlash replaces every backslash with a slash, whatever the OS.
func toSlash(val string) string { return strings.ReplaceAll(val, "\\", "/") }

// checkWithinProject rejects an output path outside the project root, a
// Windows drive path included; "" and "-" pass.
func checkWithinProject(key, val string) error {
	if val == "" || val == Disabled {
		return nil
	}
	clean := path.Clean(toSlash(val))
	drive := len(clean) >= 2 && clean[1] == ':' && ('a' <= clean[0] && clean[0] <= 'z' || 'A' <= clean[0] && clean[0] <= 'Z')
	if clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) || drive {
		return fmt.Errorf("%s %q must stay inside the project - generated code is imported as `<module>/<path>`, which cannot name a directory outside the module", key, val)
	}
	return nil
}
