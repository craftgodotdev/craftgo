package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

// parseArgs parses the flags of a subcommand, which come before its one
// optional path, and returns the path, or def when args name none. `-h` and
// `--help` print the command's usage and return errHelpRequested; a bad flag
// or argument returns a [usageError].
func parseArgs(fs *flag.FlagSet, args []string, def string) (string, error) {
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Println("Usage:\n" + commandUsage[fs.Name()])
			return "", errHelpRequested
		}
		return "", badArgs(fs, "%w", err)
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return def, nil
	}
	for _, a := range rest[1:] {
		if strings.HasPrefix(a, "-") {
			return "", badArgs(fs, "flag %q follows the path - flags go before it", a)
		}
	}
	if len(rest) > 1 {
		return "", badArgs(fs, "too many positional arguments (got %d, want at most 1)", len(rest))
	}
	return rest[0], nil
}

// usageError is a bad flag or argument to a command; main prints it with the
// command's usage.
type usageError struct {
	error
	usage string
}

// badArgs returns the usageError of the command fs parses, its message
// prefixed with the command's name.
func badArgs(fs *flag.FlagSet, format string, args ...any) usageError {
	return usageError{fmt.Errorf(fs.Name()+": "+format, args...), commandUsage[fs.Name()]}
}

// errHelpRequested reports that `-h`/`--help` printed the command's usage;
// main exits 0 on it.
var errHelpRequested = errors.New("help requested")

// errFilesDiffer reports that `fmt -l` listed files; main exits 1 on it.
var errFilesDiffer = errors.New("files differ from their canonical format")
