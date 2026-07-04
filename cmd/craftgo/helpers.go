// Cross-subcommand CLI helpers.
package main

import (
	"errors"
	"flag"
	"fmt"
)

// parseFlagError translates a [flag.ContinueOnError] result into the
// project's error contract: `-h` / `--help` is a clean exit with no noisy
// "flag: help requested" wrapper (it returns the errHelpRequested sentinel),
// every other parse error is prefixed with the subcommand name so the user
// sees `gen: …` / `init: …`.
func parseFlagError(subcommand string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, flag.ErrHelp) {
		// The flag package already printed Usage; signalling
		// "command exited cleanly" is the right shape for the
		// caller. Returning nil would silently fall through into
		// the rest of runGen / runInit, so use a sentinel that
		// short-circuits the dispatcher in main().
		return errHelpRequested
	}
	return fmt.Errorf("%s: %w", subcommand, err)
}

// errHelpRequested is the sentinel returned by [parseFlagError] when
// the user passed `-h`/`--help`. main() recognises it and exits 0
// without emitting "craftgo: …" prefix noise.
var errHelpRequested = errors.New("help requested")
