package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"
)

// parseArgs parses the flags of a subcommand, which come before its one
// optional path, and returns the path, or def when args name none. `-h` and
// `--help` return errHelpRequested.
func parseArgs(fs *flag.FlagSet, args []string, def string) (string, error) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return "", errHelpRequested
		}
		return "", fmt.Errorf("%s: %w", fs.Name(), err)
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return def, nil
	}
	for _, a := range rest[1:] {
		if strings.HasPrefix(a, "-") {
			return "", fmt.Errorf("%s: flag %q follows the path - flags go before it", fs.Name(), a)
		}
	}
	if len(rest) > 1 {
		return "", fmt.Errorf("%s: too many positional arguments (got %d, want at most 1)", fs.Name(), len(rest))
	}
	return rest[0], nil
}

// errHelpRequested reports that the flag package already printed usage for
// `-h`/`--help`; main exits 0 on it.
var errHelpRequested = errors.New("help requested")

// errFilesDiffer reports that `fmt -l` listed files; main exits 1 on it.
var errFilesDiffer = errors.New("files differ from their canonical format")
