// Package codegen runs a generation pass: it holds the language-target
// catalogue and calls each target in order.
//
// The emitters live one level down, one package per target:
//
//	codegen/golang   Go source
//	codegen/docs     the OpenAPI projection
//
// A target reads the analysed [semantic.Project] and writes its own
// artefacts; no target reads another's code, and nothing here is shared
// between them. What they have in common sits one layer lower - the
// language-independent model in [semantic] and the leaf catalogues below
// it (prims, idents, wire, route, errcat, strfmt) - so a fact two
// targets must agree on belongs there, not in this package.
//
// Adding a language is one row in [LangTargets], one name in
// [config.SupportedLangs], and one package beside golang. The two lists
// are asserted to match, so a missing half fails a test rather than
// silently generating nothing.
//
// Go is the only language target; the OpenAPI projection is the other
// reader of the shared model.
package codegen

import (
	"fmt"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/codegen/docs"
	"github.com/craftgodotdev/craftgo/internal/codegen/golang"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// LangTarget generates the event artefacts for one language.
type LangTarget struct {
	// Lang is the value `events.targets[].lang` carries.
	Lang string
	// Generate writes the target's artefacts. outDir is the target's
	// configured destination, relative to projectRoot.
	Generate func(proj *semantic.Project, cfg *config.Config, projectRoot, outDir string) error
	// OutputNotes reports what the target found in its output and could
	// not account for.
	OutputNotes func(proj *semantic.Project, cfg *config.Config, projectRoot string) []string
}

// LangTargets is the closed set of supported languages. It must match
// [config.SupportedLangs].
var LangTargets = []LangTarget{
	// The Go target places its artefacts through the project-wide
	// `output:` block, so it reads no per-target layout.
	{Lang: config.LangGo, Generate: golang.GenerateEventTarget, OutputNotes: golang.EventOutputNotes},
}

// TargetDocs selects the document projections. The language targets are
// named by [config.SupportedLangs].
const TargetDocs = "docs"

// SelectableTargets is everything `--target` accepts, in run order.
func SelectableTargets() []string {
	return append(append([]string{}, config.SupportedLangs...), TargetDocs)
}

// Generate runs a whole generation pass for proj under projectRoot: the
// Go pipeline, then every configured event language target, then the
// OpenAPI projection.
//
// targets narrows the run to the named ones; empty runs everything. A
// target that does not run also does not prune, so a narrowed pass never
// deletes another target's output.
func Generate(proj *semantic.Project, cfg *config.Config, projectRoot string, targets ...string) error {
	sel, err := selection(targets)
	if err != nil {
		return err
	}
	// The design is validated whatever is being generated: a design that
	// cannot produce a correct document is not one to emit code from.
	if err := validate(proj, cfg); err != nil {
		return err
	}
	// Nothing is written until every file this run regenerates is known
	// to be this design's to write.
	outs := plannedOutputs(proj, cfg, projectRoot, sel)
	if err := checkClaims(outs, cfg, proj.Root, projectRoot); err != nil {
		return err
	}
	if sel[config.LangGo] {
		if err := golang.Generate(proj, cfg, projectRoot); err != nil {
			return err
		}
	}
	if err := generateEventTargets(proj, cfg, projectRoot, sel); err != nil {
		return err
	}
	if sel[TargetDocs] {
		if err := GenerateDocuments(proj, cfg, projectRoot); err != nil {
			return err
		}
	}
	// The sweep reads what the LAST run claimed, and the record is filed
	// last because it overwrites that list.
	if err := pruneClaims(outs, proj.Root); err != nil {
		return err
	}
	return recordClaims(outs, cfg, proj.Root)
}

// selection turns the requested names into a lookup, rejecting anything
// unknown so a typo never silently generates less than asked.
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

// validate runs the checks that must reject a design before any file is
// written: malformed security schemes, and operationId / component-schema
// name collisions.
func validate(proj *semantic.Project, cfg *config.Config) error {
	if errs := docs.ValidateSecuritySchemes(cfg); len(errs) > 0 {
		return fmt.Errorf("security scheme errors:\n  %s", strings.Join(errs, "\n  "))
	}
	return docs.ValidateOpenAPI(proj, cfg)
}

// GenerateDocuments writes the OpenAPI projection, a pure function of the
// design that reads no generated file.
func GenerateDocuments(proj *semantic.Project, cfg *config.Config, projectRoot string) error {
	if err := docs.GenerateOpenAPI(proj, cfg, projectRoot); err != nil {
		return fmt.Errorf("openapi: %w", err)
	}
	return nil
}

// GenerateEventTargets runs every configured, enabled language target.
// A project whose design declares no event generates nothing.
func GenerateEventTargets(proj *semantic.Project, cfg *config.Config, projectRoot string) error {
	sel, _ := selection(nil)
	outs := plannedEventOutputs(proj, cfg, projectRoot, sel)
	if err := checkClaims(outs, cfg, proj.Root, projectRoot); err != nil {
		return err
	}
	if err := generateEventTargets(proj, cfg, projectRoot, sel); err != nil {
		return err
	}
	if err := pruneClaims(outs, proj.Root); err != nil {
		return err
	}
	return recordClaims(outs, cfg, proj.Root)
}

// generateEventTargets is [GenerateEventTargets] narrowed to a selection.
func generateEventTargets(proj *semantic.Project, cfg *config.Config, projectRoot string, sel map[string]bool) error {
	// A design with no event still runs every target: the artefacts of an
	// event the design used to declare are exactly what has to go, and a
	// target that does not run also claims nothing - so nothing would
	// prune the descriptor left on disk for a contract nobody declares.
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

// OutputNotes reports what every enabled target found in its output and
// could not account for.
func OutputNotes(proj *semantic.Project, cfg *config.Config, projectRoot string) []string {
	var out []string
	for _, target := range LangTargets {
		if target.OutputNotes == nil {
			continue
		}
		out = append(out, target.OutputNotes(proj, cfg, projectRoot)...)
	}
	return out
}
