// Package codegen runs a generation pass over an analysed design: it
// validates the design, runs the selected targets (codegen/golang, the event
// language targets, codegen/docs), then deletes every generated file the pass
// did not write from the directories they regenerate into. A fact two targets
// share lives in [semantic] or a leaf package, not here.
package codegen

import (
	"fmt"
	"iter"
	"slices"
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
	// plan lists the directories generate writes into, with the headers of its files, and the
	// files it writes.
	plan func(proj *semantic.Project, projectRoot, outDir string) (dirs map[string][]string, files []string)
}

// langTargets holds one row per language in [config.SupportedLangs].
var langTargets = []langTarget{
	{lang: config.LangGo, generate: golang.GenerateEventTarget, plan: golang.EventPlan},
}

// eventTargets yields the event language targets the manifest enables, each with the directory
// it writes under.
func eventTargets(cfg *config.Config) iter.Seq2[langTarget, string] {
	return func(yield func(langTarget, string) bool) {
		for _, target := range langTargets {
			cfgTarget, ok := cfg.Events.TargetFor(target.lang)
			if !ok || !cfgTarget.Enabled() {
				continue
			}
			if !yield(target, cfgTarget.Out) {
				return
			}
		}
	}
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
	return prune(plan(in, cfg, projectRoot, sel))
}

// plan lists the directories the selected targets regenerate into, each with the headers of the
// files written there, and every file the targets write, selected or not: a target a narrowed run
// skips still owns its files.
func plan(in Inputs, cfg *config.Config, projectRoot string, sel map[string]bool) ([]sweepDir, map[string]bool) {
	headers := map[string][]string{}
	written := map[string]bool{}
	take := func(selected bool, dirs map[string][]string, files []string) {
		if selected {
			for dir, hs := range dirs {
				headers[dir] = append(headers[dir], hs...)
			}
		}
		for _, file := range files {
			written[file] = true
		}
	}
	dirs, files := golang.Plan(in.Design, in.Protos, cfg, projectRoot)
	take(sel[config.LangGo], dirs, files)
	for target, outDir := range eventTargets(cfg) {
		dirs, files := target.plan(in.Design, projectRoot, outDir)
		take(sel[target.lang], dirs, files)
	}
	dirs, files = docs.Plan(in.Design, cfg, projectRoot)
	take(sel[targetDocs], dirs, files)
	for dir, hs := range headers {
		if slices.ContainsFunc(hs, func(h string) bool { return slices.Contains(craftgoHeaders, h) }) {
			headers[dir] = append(hs, craftgoHeaders...)
		}
	}
	return owned(headers, projectRoot), written
}

// craftgoHeaders open the files craftgo's targets write. A directory one of them regenerates into
// is craftgo's, so its sweep also takes a stale file another target left there.
var craftgoHeaders = []string{golang.GeneratedHeader, docs.GeneratedHeader}

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
	for target, outDir := range eventTargets(cfg) {
		if !sel[target.lang] {
			continue
		}
		if err := target.generate(proj, cfg, projectRoot, outDir); err != nil {
			return fmt.Errorf("events(%s): %w", target.lang, err)
		}
	}
	return nil
}

// OutputNotes reports what the Go output holds that the run cannot account for.
func OutputNotes(in Inputs, cfg *config.Config, projectRoot string) []string {
	return golang.OutputNotes(in.Design, in.Protos, cfg, projectRoot)
}
