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

// langTarget is one language's row in the target catalogue.
type langTarget struct {
	// lang is the `events.targets[].lang` value that selects the row.
	lang string
	// generate writes the event artefacts into outDir, relative to projectRoot.
	generate func(proj *semantic.Project, cfg *config.Config, projectRoot, outDir string) error
}

// langTargets holds one row per language in [config.SupportedLangs].
var langTargets = []langTarget{
	{lang: config.LangGo, generate: golang.GenerateEventTarget},
}

// targetDocs is the `--target` name of the OpenAPI document.
const targetDocs = "docs"

// SelectableTargets is everything `--target` accepts, in run order.
func SelectableTargets() []string {
	return append(append([]string{}, config.SupportedLangs...), targetDocs)
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
	if sel[targetDocs] {
		if err := docs.GenerateOpenAPI(in.Design, cfg, projectRoot); err != nil {
			return fmt.Errorf("openapi: %w", err)
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

// generateEventTargets runs the selected, enabled event language targets; a
// design with no event generates nothing.
func generateEventTargets(proj *semantic.Project, cfg *config.Config, projectRoot string, sel map[string]bool) error {
	for _, target := range langTargets {
		if !sel[target.lang] {
			continue
		}
		cfgTarget, ok := cfg.Events.TargetFor(target.lang)
		if !ok || !cfgTarget.Enabled() {
			continue
		}
		if err := target.generate(proj, cfg, projectRoot, cfgTarget.Out); err != nil {
			return fmt.Errorf("events(%s): %w", target.lang, err)
		}
	}
	return nil
}

// OutputNotes reports what the Go output holds that the run cannot account for.
func OutputNotes(in Inputs, cfg *config.Config, projectRoot string) []string {
	return golang.OutputNotes(in.Design, in.Protos, cfg, projectRoot)
}
