// Command craftgo is the CLI entrypoint that drives the design-first
// pipeline: locate the project manifest, parse every `.craftgo` source
// file, run semantic analysis, and dispatch each codegen artefact.
//
// Usage:
//
//	craftgo init [path] [-package <module>]
//	craftgo gen  [-f <design-folder>] [-c|--context <project-root>] [path]
//
// `init` scaffolds a fresh design folder at <path> (default `design`).
// The path argument IS the design folder - manifest + sample `.craftgo`
// files land flat inside it. Existing files are never overwritten so
// re-running on a populated directory fills only the gaps.
//
// `gen` resolves the design folder one of two ways: with `-f` it uses
// the supplied path directly; without it walks upward from <path> (or
// cwd) looking for a craftgo.design.yaml, probing direct subdirs of
// any name at each level. The project root the `output:` paths
// resolve against is `-c <root>` when given, else cwd in the `-f`
// flow, else the parent of the manifest folder (legacy compat).
package main

import (
	"errors"
	"fmt"
	"os"
)

// version is the CLI's reported version. Kept as a build-time constant for
// now; release tooling can override via `-ldflags="-X main.version=..."`.
const version = "1.3.3"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "gen":
		err = runGen(os.Args[2:])
	case "init":
		err = runInit(os.Args[2:])
	case "fmt":
		err = runFmt(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println(version)
		return
	case "help", "--help", "-h":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "craftgo: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err == nil {
		return
	}
	// `-h` / `--help` returns this sentinel - flag package already
	// printed the per-subcommand usage; exit 0 without piling on
	// our "craftgo: …" prefix.
	if errors.Is(err, errHelpRequested) {
		return
	}
	fmt.Fprintln(os.Stderr, "craftgo: "+err.Error())
	os.Exit(1)
}

// usage prints a short command summary to stdout. Verbose enough to remind
// returning users of the positional-path convention but not so detailed that
// it becomes a maintenance burden - full docs live in the README.
func usage() {
	fmt.Println(`craftgo - design-first Go API framework

Usage:
  craftgo init [path]
                          Scaffold a design folder at <path> (default: 'design').
                          The supplied path IS the design folder - the manifest
                          (craftgo.design.yaml) lands flat inside it. The Go
                          module path is read from go.mod at gen time, so init
                          itself does not need a -package flag.

  craftgo gen [-f <design-folder>] [-c|--context <project-root>] [path]
                          Generate types, handlers, routes, OpenAPI from
                          .craftgo files. Flags:
                            -f, --folder   path to the folder holding
                                           craftgo.design.yaml (skips walk-up)
                            -c, --context  project root the output: paths
                                           resolve against (defaults to cwd
                                           when -f is given, otherwise to
                                           the parent of the manifest dir)
                          Without -f, walks upward from <path> (or cwd) for
                          craftgo.design.yaml, probing direct subdirs (any
                          name) at each level. The Go module path is read
                          from go.mod, walking up from the project root -
                          run "go mod init <module>" first if it does not
                          exist yet.

  craftgo fmt [path] [-l] [-w]
                          Canonical-format .craftgo files (default: write back)
  craftgo version         Print the CLI version
  craftgo help            Show this message

For 'fmt', path may be a single file or a directory (recursed for *.craftgo).`)
}
