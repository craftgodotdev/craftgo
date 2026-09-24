// Command craftgo scaffolds a design folder (init), generates a Go project and
// its OpenAPI document from it (gen), and formats its design files (fmt).
package main

import (
	"errors"
	"fmt"
	"os"
)

// version is the reported version; release builds set it with
// `-ldflags="-X main.version=<tag>"`, which needs a var, not a const.
var version = "1.9.0"

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
	if errors.Is(err, errHelpRequested) {
		return
	}
	if !errors.Is(err, errFilesDiffer) {
		fmt.Fprintln(os.Stderr, "craftgo: "+err.Error())
	}
	os.Exit(1)
}

// usage prints the command summary to stdout.
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
                          .craftgo files, and the pb code, gRPC server layer
                          and logic stubs from .proto files in the same
                          folder (protoc-gen-go and protoc-gen-go-grpc run
                          through "go tool"; pin them with "go get -tool").
                          Flags:
                            -f, --folder   path to the folder holding
                                           craftgo.design.yaml (skips walk-up)
                            --target       generate only the named target
                                           (go, docs); repeatable,
                                           default all. A
                                           narrowed run leaves the other
                                           targets' output untouched.
                            -c, --context  project root the output: paths
                                           resolve against (default: the
                                           parent of the design folder)
                          Without -f, walks upward from <path> (or cwd) for
                          craftgo.design.yaml, probing direct subdirs (any
                          name) at each level. The Go module path is read
                          from go.mod, walking up from the project root -
                          run "go mod init <module>" first if it does not
                          exist yet.

  craftgo fmt [-l] [-w] [path]
                          Canonical-format the design files (.craftgo, .cg)
                          under <path> (default: cwd), or the file it names.
                          A file with an error is reported and left untouched.
                          Flags:
                            -l   list the files that differ, write nothing,
                                 and exit 1 if any differ
                            -w   write the result back (the default; with
                                 -l, list and write)

  craftgo version         Print the CLI version
  craftgo help            Show this message`)
}
