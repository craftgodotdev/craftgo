// Command craftgo-lsp implements a Language Server Protocol server for
// the CraftGo DSL. It reads JSON-RPC over stdin and writes responses to
// stdout, the standard transport for editor integrations.
//
// Usage:
//
//	craftgo-lsp            # serve LSP over stdin/stdout
//	craftgo-lsp -version   # print the binary version and exit
//
// The server reuses the same parser and semantic analyser as the
// `craftgo` CLI, so live diagnostics in the editor exactly match what
// `craftgo gen` would report on the same source.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/dropship-dev/craftgo/internal/lsp"
)

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	// `-stdio` is accepted but ignored. The server only speaks LSP over
	// stdin/stdout, so the flag is redundant - but vscode-languageclient
	// (and several other LSP clients) pass it unconditionally to signal
	// transport choice. Defining the flag here keeps the standard `flag`
	// package from rejecting the binary with "flag provided but not
	// defined" and exit code 2.
	_ = flag.Bool("stdio", false, "use stdio transport (default and only mode; accepted for client compatibility)")
	flag.Parse()
	if *showVersion {
		fmt.Println(lsp.Version)
		return
	}
	if err := lsp.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "craftgo-lsp:", err)
		os.Exit(1)
	}
}
