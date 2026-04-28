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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the in-memory shape of `craftgo.design.yaml`. Field tags match
// the camelCase keys documented in the project README.
type Config struct {
	Package   string    `yaml:"package"`
	Output    Output    `yaml:"output"`
	OpenAPI   OpenAPI   `yaml:"openapi"`
	Templates Templates `yaml:"templates"`
}

// Output groups every generated-artefact destination. Directory paths
// (Types, Handler, Routes, Logic, Client) are appended with `/<service>`
// per service at codegen time. File paths (Main, Svccontext, OpenAPI) point
// at the exact file that will be written. All paths are relative to the
// **project root** (the parent of the design folder).
type Output struct {
	Types      string `yaml:"types"`
	Handler    string `yaml:"handler"`
	Routes     string `yaml:"routes"`
	Logic      string `yaml:"logic"`
	Client     string `yaml:"client"`
	Main       string `yaml:"main"`
	Svccontext string `yaml:"svccontext"`
	OpenAPI    string `yaml:"openapi"`
	// Middleware is the scaffold-once output dir for middleware
	// implementation files. The corresponding type declarations live
	// next to svccontext.go (see GenerateMiddlewares).
	Middleware string `yaml:"middleware"`
}

// OpenAPI carries metadata that surfaces in the generated specification's
// info / servers blocks. BasePath is also used by the runtime to compute the
// final route string for each method. SecuritySchemes is a name → scheme
// map that powers the `@security(name)` cross-check: any DSL reference
// must resolve to a key here when the map is non-empty.
type OpenAPI struct {
	Title           string                    `yaml:"title"`
	Version         string                    `yaml:"version"`
	BasePath        string                    `yaml:"basePath"`
	SecuritySchemes map[string]SecurityScheme `yaml:"securitySchemes"`
}

// SecurityScheme is the project-side projection of an OpenAPI 3.1
// security scheme object. We retain only the fields the codegen needs to
// validate references and (later) emit accurate OpenAPI components; the
// full OpenAPI shape is intentionally not modelled here so the manifest
// stays small. Fields marked with `omitempty` allow concise YAML.
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
}

// Templates configures the project-local template override directory.
type Templates struct {
	Dir string `yaml:"dir"`
}

// Filename is the canonical project manifest file name. Find walks parent
// directories looking for it, optionally peeking into a child `design/`
// directory at each level.
const Filename = "craftgo.design.yaml"

// Find walks upward from `start` until it locates a [Filename]. At every
// candidate directory two paths are checked, in order: the directory
// itself, then its child `design/` subdirectory — that lets users invoke
// `craftgo gen` from either the project root or the design folder
// without thinking about it.
//
// On success it returns the loaded [*Config], the absolute path of the
// project root (the parent of the design folder), and the absolute path
// of the design folder itself. The design folder is also where every
// `.craftgo` source file lives.
func Find(start string) (*Config, string, string, error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return nil, "", "", err
	}
	dir := abs
	for {
		// Direct hit — manifest sits in dir.
		if path := filepath.Join(dir, Filename); fileExists(path) {
			cfg, err := Load(path)
			if err != nil {
				return nil, "", "", err
			}
			return cfg, filepath.Dir(dir), dir, nil
		}
		// Sibling design folder — manifest sits in dir/design/.
		if path := filepath.Join(dir, "design", Filename); fileExists(path) {
			cfg, err := Load(path)
			if err != nil {
				return nil, "", "", err
			}
			designDir := filepath.Join(dir, "design")
			return cfg, dir, designDir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, "", "", errors.New("craftgo.design.yaml not found in any parent directory or sibling design/")
		}
		dir = parent
	}
}

// fileExists is a small wrapper around os.Stat that ignores its error,
// returning true only when the path resolves to a regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// Load parses the manifest at `path`, validates required fields, applies
// defaults to optional ones, and returns the resulting [*Config].
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	cfg.applyDefaults()
	return &cfg, nil
}

// validate checks the small set of fields that have no sensible default.
// Currently only `package` is mandatory — everything else falls back to
// project-relative defaults.
func (c *Config) validate() error {
	if strings.TrimSpace(c.Package) == "" {
		return errors.New("config: 'package' (Go module path) is required")
	}
	return nil
}

// applyDefaults fills in any blank optional path with the framework's
// recommended location. Mirrors the README "Configuration" section so that
// projects can run with just `package:` set.
func (c *Config) applyDefaults() {
	if c.Output.Types == "" {
		c.Output.Types = "./internal/types"
	}
	if c.Output.Handler == "" {
		c.Output.Handler = "./internal/handler"
	}
	if c.Output.Routes == "" {
		c.Output.Routes = "./internal/routes"
	}
	if c.Output.Logic == "" {
		c.Output.Logic = "./internal/logic"
	}
	if c.Output.Client == "" {
		c.Output.Client = "./client"
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
	if c.Templates.Dir == "" {
		c.Templates.Dir = "./.craftgo/templates"
	}
}
