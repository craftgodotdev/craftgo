// Command craftgo-lsp is the craftgo language server.
//
//	craftgo-lsp            # serve LSP over stdin/stdout
//	craftgo-lsp -version   # print the version and exit
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/craftgodotdev/craftgo/internal/lsp"
)

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	// -stdio is accepted and ignored: vscode-languageclient passes it, and flag
	// rejects an undefined flag.
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
