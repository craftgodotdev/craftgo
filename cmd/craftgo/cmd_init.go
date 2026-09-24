package main

import (
	_ "embed"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/protodesign"
)

// initManifest is the starter manifest init writes.
//
//go:embed templates/craftgo.design.yaml.tmpl
var initManifest []byte

// runInit writes a starter craftgo.design.yaml into the design folder args[0]
// (default `design`), creating the folder, and leaves an existing manifest alone.
func runInit(args []string) error {
	target, err := parseArgs(flag.NewFlagSet("init", flag.ContinueOnError), args, "design")
	if err != nil {
		return err
	}
	designDir, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(designDir, 0o755); err != nil {
		return err
	}

	dest := filepath.Join(designDir, config.Filename)
	if _, err := os.Stat(dest); err == nil {
		fmt.Printf("craftgo: %s already exists, nothing to do\n", dest)
		return nil
	}
	if err := os.WriteFile(dest, initManifest, 0o644); err != nil {
		return err
	}
	fmt.Printf("craftgo: wrote %s\n", dest)
	fmt.Println("next steps:")
	fmt.Printf("  1. ensure `go.mod` exists at your project root (`go mod init <module>`)\n")
	fmt.Printf("  2. add at least one .craftgo file in %s declaring `package X` (types, services),\n", target)
	fmt.Printf("     or a .proto declaring a gRPC service (then pin the plugins once with\n")
	fmt.Printf("     `%s`)\n", protodesign.PluginInstall)
	fmt.Printf("  3. run `craftgo gen -f %s` to generate types, handlers, routes, openapi, and the gRPC layer\n", target)
	return nil
}
