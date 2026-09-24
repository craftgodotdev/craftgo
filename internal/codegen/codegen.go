// Package codegen runs a generation pass over an analysed design: it
// validates the design, runs the selected targets (codegen/golang, the event
// language targets, codegen/docs), then deletes every generated file the pass
// did not write from the directories they regenerate into. A fact two targets
// share lives in [semantic] or a leaf package, not here.
package codegen

import (
	"fmt"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/codegen/docs"
	"github.com/craftgodotdev/craftgo/internal/codegen/golang"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/protodesign"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// Inputs is what a pass generates from: the analysed design, and the
// compiled proto set, nil when the design folder holds no `.proto`.
type Inputs struct {
	Design *semantic.Project
	Protos *protodesign.Set
}

// LangTarget is one language's row in the target catalogue.
type LangTarget struct {
	// Lang is the `events.targets[].lang` value that selects the row.
	Lang string
	// Generate writes the event artefacts into outDir, relative to projectRoot.
	Generate func(proj *semantic.Project, cfg *config.Config, projectRoot, outDir string) error
	// OutputNotes reports what the language's output holds that the run
	// cannot account for.
	OutputNotes func(proj *semantic.Project, protos *protodesign.Set, cfg *config.Config, projectRoot string) []string
}

// LangTargets holds one row per language in [config.SupportedLangs].
var LangTargets = []LangTarget{
	{Lang: config.LangGo, Generate: golang.GenerateEventTarget, OutputNotes: golang.EventOutputNotes},
}

// TargetDocs is the `--target` name of the OpenAPI document.
const TargetDocs = "docs"

// SelectableTargets is everything `--target` accepts, in run order.
func SelectableTargets() []string {
	return append(append([]string{}, config.SupportedLangs...), TargetDocs)
}

// Generate runs a pass for in under projectRoot: Go, the event targets, then
// OpenAPI. targets narrows the run and its sweep; none selects every target.
func Generate(in Inputs, cfg *config.Config, projectRoot string, targets ...string) error {
	sel, err := selection(targets)
	if err != nil {
		return err
	}
	if err := validate(in, cfg); err != nil {
		return err
	}
	if err := emit(in, cfg, projectRoot, sel); err != nil {
		return err
	}
	return prune(outputDirs(cfg, projectRoot, sel), regeneratedFiles(in, cfg, projectRoot))
}

// emit runs the selected targets in order, without the sweep.
func emit(in Inputs, cfg *config.Config, projectRoot string, sel map[string]bool) error {
	if sel[config.LangGo] {
		if err := golang.Generate(in.Design, in.Protos, cfg, projectRoot); err != nil {
			return err
		}
	}
	if err := generateEventTargets(in.Design, cfg, projectRoot, sel); err != nil {
		return err
	}
	if sel[TargetDocs] {
		if err := GenerateDocuments(in.Design, cfg, projectRoot); err != nil {
			return err
		}
	}
	return nil
}

// selection turns the requested names into a set, rejecting an unknown
// name; no name selects every target.
func selection(targets []string) (map[string]bool, error) {
	known := map[string]bool{}
	for _, name := range SelectableTargets() {
		known[name] = true
	}
	if len(targets) == 0 {
		return known, nil
	}
	sel := map[string]bool{}
	for _, name := range targets {
		if !known[name] {
			return nil, fmt.Errorf("unknown target %q - use %s", name, strings.Join(SelectableTargets(), ", "))
		}
		sel[name] = true
	}
	return sel, nil
}

// validate rejects, before any file is written, a design whose document
// would be invalid or whose outputs would overwrite each other.
func validate(in Inputs, cfg *config.Config) error {
	if errs := docs.ValidateSecuritySchemes(cfg); len(errs) > 0 {
		return fmt.Errorf("security scheme errors:\n  %s", strings.Join(errs, "\n  "))
	}
	if err := docs.ValidateOpenAPI(in.Design, cfg); err != nil {
		return err
	}
	return golang.ValidateProtoOutputs(in.Design, in.Protos, cfg)
}

// GenerateDocuments writes the OpenAPI document.
func GenerateDocuments(proj *semantic.Project, cfg *config.Config, projectRoot string) error {
	if err := docs.GenerateOpenAPI(proj, cfg, projectRoot); err != nil {
		return fmt.Errorf("openapi: %w", err)
	}
	return nil
}

// GenerateEventTargets runs every enabled event language target, then
// sweeps their directories. A design with no event generates nothing.
func GenerateEventTargets(proj *semantic.Project, cfg *config.Config, projectRoot string) error {
	sel, _ := selection(nil)
	if err := generateEventTargets(proj, cfg, projectRoot, sel); err != nil {
		return err
	}
	return prune(eventOutputDirs(cfg, projectRoot, sel), regeneratedFiles(Inputs{Design: proj}, cfg, projectRoot))
}

// generateEventTargets runs the selected, enabled event language targets.
func generateEventTargets(proj *semantic.Project, cfg *config.Config, projectRoot string, sel map[string]bool) error {
	for _, target := range LangTargets {
		if !sel[target.Lang] {
			continue
		}
		cfgTarget, ok := cfg.Events.TargetFor(target.Lang)
		if !ok || !cfgTarget.Enabled() {
			continue
		}
		if err := target.Generate(proj, cfg, projectRoot, cfgTarget.Out); err != nil {
			return fmt.Errorf("events(%s): %w", target.Lang, err)
		}
	}
	return nil
}

// OutputNotes reports what each language target finds in its output and
// cannot account for.
func OutputNotes(in Inputs, cfg *config.Config, projectRoot string) []string {
	var out []string
	for _, target := range LangTargets {
		if target.OutputNotes == nil {
			continue
		}
		out = append(out, target.OutputNotes(in.Design, in.Protos, cfg, projectRoot)...)
	}
	return out
}
