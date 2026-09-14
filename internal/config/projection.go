// A projection: a manifest that generates from a design source it does
// not contain, so several deployables build from one design.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// sourceOwnedKeys are the manifest keys a projection inherits from its
// design source rather than stating itself. Each one places or names a
// file in the contract half, which every deployable of the design shares:
// two projections stating them apart would write one library twice, from
// two directories and under two spellings.
var sourceOwnedKeys = []struct {
	key string
	set func(*Config) bool
}{
	{"output.types", func(c *Config) bool { return c.Output.Types != "" }},
	{"output.fileCase", func(c *Config) bool { return c.Output.FileCase != "" }},
	{"events.targets", func(c *Config) bool { return len(c.Events.Targets) > 0 }},
}

// resolveDesignSource turns `design.from` / `design.root` into the
// absolute paths the rest of craftgo reads, and folds the source
// manifest's contract half into this one. manifestDir is the folder
// holding the manifest being loaded, which both paths are relative to.
//
// A manifest with no `design.from` is left untouched: its design sits
// beside it, and it owns every key itself.
func (c *Config) resolveDesignSource(manifestDir string, loading []string) error {
	if c.Design.From == "" {
		if c.Design.Root != "" {
			return fmt.Errorf("design.root is set without design.from - a project root without a design source is this project's own, and that is the `-c` flag")
		}
		return nil
	}
	source, err := filepath.Abs(filepath.Join(manifestDir, filepath.FromSlash(c.Design.From)))
	if err != nil {
		return err
	}
	if info, err := os.Stat(source); err != nil || !info.IsDir() {
		return fmt.Errorf("design.from %q is not a directory (looked in %s)", c.Design.From, source)
	}
	if own := designFilesUnder(manifestDir); own != "" {
		return fmt.Errorf("this manifest names a design source (design.from %q) and holds a design of its own (%s) - a projection reads one design, so move the source or drop design.from",
			c.Design.From, own)
	}
	// The project root a design generates with is a CLI argument (`-c`),
	// so craftgo cannot see it from the folder: a design at
	// `contracts/upstream/design` may be generated with `contracts` as
	// its root as readily as with `contracts/upstream`, and guessing
	// wrong yields an import path that does not exist. It is stated.
	if c.Design.Root == "" {
		return fmt.Errorf("design.root is required beside design.from - it is the project root the design source is generated with (`craftgo gen -c <root>`), which craftgo cannot read off the folder")
	}
	root, err := filepath.Abs(filepath.Join(manifestDir, filepath.FromSlash(c.Design.Root)))
	if err != nil {
		return err
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return fmt.Errorf("design.root %q is not a directory (looked in %s)", c.Design.Root, root)
	}
	manifest := filepath.Join(source, Filename)
	if !fileExists(manifest) {
		return fmt.Errorf("design.from %q holds no %s - the source is a craftgo project, and its manifest is what places the contract half this one shares", c.Design.From, Filename)
	}
	src, err := load(manifest, loading)
	if err != nil {
		return fmt.Errorf("design source %s: %w", filepath.ToSlash(c.Design.From), err)
	}
	if src.IsProjection() {
		return fmt.Errorf("design.from %q names a projection, not a design - point it at the manifest that holds the `.craftgo` files", c.Design.From)
	}
	for _, owned := range sourceOwnedKeys {
		if owned.set(c) {
			return fmt.Errorf("%s is the design source's to set - this manifest is a projection of %s, and both would write one contract library; remove the key",
				owned.key, filepath.ToSlash(c.Design.From))
		}
	}
	c.SourceDesign = source
	c.Library.Root = root
	c.inherit(src)
	return nil
}

// inherit takes the contract half from the design source: where the
// payload types and the event artefacts land, and the OpenAPI metadata
// describing the design. A projection may state its own metadata, and
// what it states wins; the placement keys it may not state at all.
func (c *Config) inherit(src *Config) {
	c.Output.Types = src.Output.Types
	c.Output.FileCase = src.Output.FileCase
	c.Events.Targets = src.Events.Targets
	if c.OpenAPI.Title == "" {
		c.OpenAPI.Title = src.OpenAPI.Title
	}
	if c.OpenAPI.Version == "" {
		c.OpenAPI.Version = src.OpenAPI.Version
	}
	if c.OpenAPI.Description == "" {
		c.OpenAPI.Description = src.OpenAPI.Description
	}
	if c.OpenAPI.BasePath == "" {
		c.OpenAPI.BasePath = src.OpenAPI.BasePath
	}
	// The schemes are the truth source for `@security(name)` in the
	// shared design: a projection analysing it without them rejects
	// every reference the design makes.
	if len(c.OpenAPI.SecuritySchemes) == 0 {
		c.OpenAPI.SecuritySchemes = src.OpenAPI.SecuritySchemes
	}
}

// designFilesUnder returns the first `.craftgo` file found under dir, or
// "" when it holds none. A design folder nests freely, so the search
// does too; the directories [probeDesignSubdirs] skips are skipped here
// for the same reason.
func designFilesUnder(dir string) string {
	found := ""
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return nil
		}
		if d.IsDir() {
			if name := d.Name(); path != dir && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !IsDesignFile(path) {
			return nil
		}
		if rel, relErr := filepath.Rel(dir, path); relErr == nil {
			found = filepath.ToSlash(rel)
		}
		return nil
	})
	return found
}

// checkServiceSelection rejects an `output.services` entry that is not a
// `<package>.<Service>` pair. Whether the pair names a service the design
// declares is checked against the analysed design, where the near misses
// can be named.
func (c *Config) checkServiceSelection() error {
	for _, entry := range c.Output.Services {
		pkg, svc, ok := strings.Cut(entry, ".")
		if !ok || pkg == "" || svc == "" || strings.Contains(svc, ".") {
			return fmt.Errorf("output.services entry %q is not a `<package>.<Service>` pair - a service is named by the DSL package declaring it", entry)
		}
	}
	return nil
}
