// craftgo check subcommand: compare the generated event contracts against a
// previously published version and report what would break its users.
package main

import (
	"flag"
	"fmt"
	"path/filepath"

	"github.com/craftgodotdev/craftgo/internal/compat"
)

// runCheck diffs the project's AsyncAPI document against an earlier copy.
// Exits non-zero when a change would break an existing publisher or
// consumer, so it can gate a release in CI.
func runCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	var against, current, manifest, ctxRoot string
	fs.StringVar(&against, "against", "", "path to the previously published asyncapi document (required)")
	fs.StringVar(&current, "current", "", "document to check (defaults to the manifest's events.asyncapi output)")
	fs.StringVar(&manifest, "f", "", "design folder holding craftgo.design.yaml (skips walk-up)")
	fs.StringVar(&ctxRoot, "c", "", "project root the output paths resolve against")
	if err := fs.Parse(args); err != nil {
		return parseFlagError("check", err)
	}
	if against == "" {
		return fmt.Errorf("check: -against is required (the previously published asyncapi document)")
	}

	if current == "" {
		cfg, root, _, err := resolveGenPaths(manifest, ctxRoot, "")
		if err != nil {
			return err
		}
		if cfg.Events.AsyncAPI == "" || cfg.Events.AsyncAPI == "-" {
			return fmt.Errorf("check: the manifest writes no asyncapi document; pass -current explicitly")
		}
		current = filepath.Join(root, cfg.Events.AsyncAPI)
	}

	prev, err := compat.Load(against)
	if err != nil {
		return fmt.Errorf("check: %w", err)
	}
	next, err := compat.Load(current)
	if err != nil {
		return fmt.Errorf("check: %w", err)
	}

	changes := compat.Compare(prev, next)
	if len(changes) == 0 {
		fmt.Println("craftgo: contracts unchanged")
		return nil
	}
	for _, c := range changes {
		fmt.Println("  " + c.String())
	}
	if compat.Breaking(changes) {
		return fmt.Errorf("check: the contracts break existing users")
	}
	fmt.Println("craftgo: changes are backward compatible")
	return nil
}
