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
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the in-memory shape of `craftgo.design.yaml`. Field tags match
// the camelCase keys documented in the project README.
type Config struct {
	Output  Output  `yaml:"output"`
	OpenAPI OpenAPI `yaml:"openapi"`

	// Package is the Go import path prefix every generated file uses
	// for its imports - the equivalent of <module>/<relPathFromGoMod>
	// for the project root. Not loaded from YAML: populated at gen
	// time by [ResolveModulePath], which walks up from the project
	// root looking for a go.mod and computes the effective import
	// path. The manifest carries no module field; go.mod's `module`
	// line is the sole source of truth.
	Package string `yaml:"-"`
}

// Output groups every generated-artefact destination. Directory paths
// (Types, Transport, Routes, Service) are appended with `/<service>`
// per service at codegen time. File paths (Main, Svccontext, OpenAPI) point
// at the exact file that will be written. All paths are relative to the
// **project root** (the parent of the design folder).
type Output struct {
	Types      string `yaml:"types"`
	Transport  string `yaml:"transport"`
	Routes     string `yaml:"routes"`
	Service    string `yaml:"service"`
	Main       string `yaml:"main"`
	Svccontext string `yaml:"svccontext"`
	OpenAPI    string `yaml:"openapi"`
	// Middleware is the scaffold-once output dir for middleware
	// implementation files. The corresponding type declarations live
	// next to svccontext.go (see GenerateProjectMiddlewares).
	Middleware string `yaml:"middleware"`
	// Config is the scaffold-once directory holding the runtime
	// configuration package (config.go + config.yaml +
	// example.config.yaml). main.go reads from `<Config>/config.yaml`
	// at boot. Defaults to `./config`.
	Config string `yaml:"config"`
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
}

// Filename is the canonical project manifest file name. Find walks parent
// directories looking for it, optionally peeking into a child `design/`
// directory at each level.
const Filename = "craftgo.design.yaml"

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
// project root (the parent of the design folder, kept for backwards
// compatibility with the existing positional-arg flow), and the
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
// is empty the parent of `designFolder` is used (legacy convention);
// pass an explicit value (typically the current working directory)
// when the design folder lives outside the project tree - the
// monorepo case where contracts/ and services/ are siblings.
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

// validate checks required manifest fields. Every required field has
// either a default or is populated post-Load (Package via go.mod), so
// there is nothing to reject; it stays as a hook for future required
// keys without re-wiring callers.
func (c *Config) validate() error { return nil }

// applyDefaults fills in any blank optional path with the framework's
// recommended location. Mirrors the README "Configuration" section so
// projects can run with an empty manifest and inherit every default.
func (c *Config) applyDefaults() {
	if c.Output.Types == "" {
		c.Output.Types = "./internal/types"
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
	if c.Output.Config == "" {
		c.Output.Config = "./config"
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
