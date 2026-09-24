package main

import (
	"errors"
	"flag"
	"fmt"
)

// parseFlagError maps a flag parse error: `-h`/`--help` becomes
// errHelpRequested, any other error is prefixed with the subcommand name.
func parseFlagError(subcommand string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, flag.ErrHelp) {
		return errHelpRequested
	}
	return fmt.Errorf("%s: %w", subcommand, err)
}

// errHelpRequested reports that the flag package already printed usage for
// `-h`/`--help`; main exits 0 on it.
var errHelpRequested = errors.New("help requested")
