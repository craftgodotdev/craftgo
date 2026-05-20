// craftgo init subcommand: scaffold a design folder + manifest.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	if perr := fs.Parse(args); perr != nil {
		return parseFlagError("init", perr)
	}
	rest := fs.Args()
	target := "design"
	switch len(rest) {
	case 0:
		// keep default
	case 1:
		target = rest[0]
	default:
		return fmt.Errorf("init: too many positional arguments (got %d, want at most 1)", len(rest))
	}

	designDir, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(designDir, 0o755); err != nil {
		return err
	}

	// Skip silently when the manifest already exists so re-running
	// init on a populated folder is a no-op.
	dest := filepath.Join(designDir, "craftgo.design.yaml")
	if _, err := os.Stat(dest); err == nil {
		fmt.Printf("craftgo: %s already exists, nothing to do\n", dest)
		return nil
	}
	if err := os.WriteFile(dest, []byte(initManifest()), 0o644); err != nil {
		return err
	}
	fmt.Printf("craftgo: wrote %s\n", dest)
	fmt.Println("next steps:")
	fmt.Printf("  1. ensure `go.mod` exists at your project root (`go mod init <module>`)\n")
	fmt.Printf("  2. add at least one .craftgo file in %s declaring `package X` (types, services)\n", target)
	fmt.Printf("  3. run `craftgo gen -f %s` to generate types, handlers, routes, openapi\n", target)
	return nil
}

// initManifest renders the starter craftgo.design.yaml. The body has
// no template variables - every value is either a default that 90%
// of projects keep or a commented hint at an optional knob. The
// Go module path is read from go.mod at gen time, so the manifest
// itself carries no `package:` field.
//
// The body lives in `templates/craftgo.design.yaml.tmpl`; edit that
// file rather than hand-rolling the YAML in Go source.
func initManifest() string {
	return renderInitTemplate("craftgo.design.yaml.tmpl", nil)
}
