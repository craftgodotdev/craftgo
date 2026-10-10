package golang

import (
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/protodesign"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// staleApplicationOutput names generated application files still on disk in a contracts
// project; the sweep never walks those directories.
func staleApplicationOutput(cfg *config.Config, projectRoot string) []string {
	if !cfg.Output.ContractsOnly() {
		return nil
	}
	var found []string
	for _, k := range outputsOf(cfg).keys() {
		if !k.application || k.dir.rel == "" || k.dir.rel == config.Disabled {
			continue
		}
		if hasGeneratedFile(k.dir.at(projectRoot)) {
			found = append(found, k.dir.rel)
		}
	}
	if main := cfg.Output.Main; main != "" && !cfg.Output.RuntimeDisabled() && fileHasPrefix(filepath.Join(projectRoot, main), scaffoldHeader) {
		found = append(found, main)
	}
	if len(found) == 0 {
		return nil
	}
	sort.Strings(found)
	return []string{"output.kind is contracts but " + strings.Join(found, ", ") +
		" still hold generated application files - delete them, or they ship with the contract library"}
}

// hasGeneratedFile reports whether dir holds a .go file craftgo wrote.
func hasGeneratedFile(dir string) bool {
	found := false
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".go" || found {
			return nil
		}
		if fileHasPrefix(path, GeneratedHeader) || fileHasPrefix(path, scaffoldHeader) {
			found = true
		}
		return nil
	})
	return found
}

// missingContainerNote reports a missing output.svccontext under `output.main: "-"`, which
// skips its scaffold although the generated code needs its ServiceContext.
func missingContainerNote(cfg *config.Config, projectRoot string) []string {
	if !cfg.Output.RuntimeDisabled() || cfg.Output.ContractsOnly() {
		return nil
	}
	dest := filepath.Join(projectRoot, cfg.Output.Svccontext)
	if _, err := os.Stat(dest); err == nil {
		return nil
	}
	return []string{"output.main is \"-\", so " + cfg.Output.Svccontext +
		" is yours to write - the generated handlers and wiring need a ServiceContext type there"}
}

// OutputNotes reports what the Go output holds that this run cannot
// account for.
func OutputNotes(proj *semantic.Project, protos *protodesign.Set, cfg *config.Config, projectRoot string) []string {
	out := staleApplicationOutput(cfg, projectRoot)
	out = append(out, missingContainerNote(cfg, projectRoot)...)
	out = append(out, staleWiringImport(cfg, projectRoot)...)
	out = append(out, scaffoldGapNotes(proj, protos, cfg, projectRoot)...)
	out = append(out, orphanedStubNotes(proj, protos, cfg, projectRoot)...)
	return append(out, protoWarnings(protos)...)
}

// protoWarnings words the proto compile's warnings as notes.
func protoWarnings(protos *protodesign.Set) []string {
	if protos == nil {
		return nil
	}
	out := make([]string, 0, len(protos.Warnings))
	for _, w := range protos.Warnings {
		out = append(out, "warning: "+w)
	}
	return out
}

// orphanedStubNotes names the output.service directories whose HTTP or gRPC service the design no
// longer declares, or names under another output.fileCase; the stubs are never swept, but the
// generated code they import is.
func orphanedStubNotes(proj *semantic.Project, protos *protodesign.Set, cfg *config.Config, projectRoot string) []string {
	if cfg.Output.ContractsOnly() {
		return nil
	}
	owned := map[string]bool{}
	// recased maps a directory a declared service's stubs take under another file case to the
	// service and the directory they take now.
	type place struct{ svc, dir string }
	recased := map[string]place{}
	own := func(svc, dir string) {
		owned[dir] = true
		for _, fileCase := range []string{idents.FileCaseSnake, idents.FileCaseKebab, idents.FileCaseCamel} {
			if old := idents.FileName(svc, fileCase); old != dir {
				recased[old] = place{svc, dir}
			}
		}
	}
	for s := range projectSegments(proj, cfg.Output.FileCase) {
		if s.group != "" {
			owned[s.dir] = true
			continue
		}
		own(s.name, s.dir)
	}
	if protos != nil {
		for _, svc := range protos.Services {
			own(svc.Name, svc.Dir)
		}
	}
	root := outputsOf(cfg).service.at(projectRoot)
	orphaned := map[string]bool{}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !fileHasPrefix(path, scaffoldHeader) {
			return nil
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return nil
		}
		if dir := filepath.ToSlash(rel); !owned[dir] {
			orphaned[dir] = true
		}
		return nil
	})
	var out []string
	for _, dir := range slices.Sorted(maps.Keys(orphaned)) {
		if now, ok := recased[dir]; ok {
			out = append(out, cfg.Output.Service+"/"+dir+" holds the logic stubs of "+now.svc+", which output.fileCase now places in "+
				cfg.Output.Service+"/"+now.dir+" - gen scaffolded fresh stubs there, so move your logic over and delete the old directory")
			continue
		}
		out = append(out, cfg.Output.Service+"/"+dir+" holds logic stubs of a service the design no longer declares - the generated code they import is gone, so move or delete them")
	}
	return out
}

// scaffoldGapNotes reports a gen-once main.go or config.go that predates the design's transports,
// gRPC or HTTP, and a pb import with no .pb.go under `output.pb: "-"`.
func scaffoldGapNotes(proj *semantic.Project, protos *protodesign.Set, cfg *config.Config, projectRoot string) []string {
	if cfg.Output.RuntimeDisabled() || cfg.Output.ContractsOnly() {
		return nil
	}
	mainPath := filepath.Join(projectRoot, cfg.Output.Main)
	configPath := outputsOf(cfg).config.at(projectRoot, "config.go")
	var out []string
	if _, err := os.Stat(mainPath); err == nil {
		if protos.HasServices() && !fileMentions(mainPath, "wiring.RegisterGRPC(") {
			out = append(out, cfg.Output.Main+" predates the gRPC services and never calls wiring.RegisterGRPC - it is generated once, so add the gRPC listener block by hand (docs/guide/grpc.md shows it)")
		}
		if !protos.HasServices() && fileMentions(mainPath, "wiring.RegisterGRPC(") {
			out = append(out, cfg.Output.Main+" still boots a gRPC listener, but the design declares no proto service and "+cfg.Output.Wiring+"/grpc.go is gone - it is generated once, so remove the gRPC block by hand (or restore the proto)")
		}
		if projectHasRoutes(proj) && !fileMentions(mainPath, "wiring.Register(") {
			out = append(out, cfg.Output.Main+" predates the HTTP routes and never calls wiring.Register - it is generated once, so add the HTTP listener block by hand (docs/guide/runtime.md shows it)")
		}
	}
	if _, err := os.Stat(configPath); err == nil && protos.HasServices() && !fileMentions(configPath, "GRPCConfig") {
		out = append(out, cfg.Output.Config+"/config.go predates the gRPC services and has no GRPCConfig - it is generated once, so add the `grpc:` block by hand (docs/guide/grpc.md shows it)")
	}
	if _, err := os.Stat(configPath); err == nil && projectHasRoutes(proj) && !fileMentions(configPath, "ServerConfig") {
		out = append(out, cfg.Output.Config+"/config.go predates the HTTP routes and has no Server or Docs config, which the HTTP listener in "+cfg.Output.Main+
			" reads - it is generated once, so add the `server:` and `docs:` blocks by hand (docs/guide/configuration.md shows them)")
	}
	if protos != nil && cfg.Output.PBDisabled() {
		for _, svc := range protos.Services {
			rel, ok := strings.CutPrefix(svc.PBImport, cfg.Package+"/")
			if !ok {
				continue
			}
			if matches, _ := filepath.Glob(filepath.Join(projectRoot, filepath.FromSlash(rel), "*.pb.go")); len(matches) == 0 {
				out = append(out, "output.pb is \"-\" and "+rel+" holds no .pb.go yet - the generated gRPC layer imports it, so run your protoc/buf pipeline before `go build`")
			}
		}
	}
	return out
}

// staleWiringImport reports a gen-once main.go calling a wiring package other than output.wiring.
func staleWiringImport(cfg *config.Config, projectRoot string) []string {
	if cfg.Output.RuntimeDisabled() || cfg.Output.ContractsOnly() {
		return nil
	}
	mainPath := filepath.Join(projectRoot, cfg.Output.Main)
	wiringImport := outputsOf(cfg).wiring.pkg
	if !fileMentions(mainPath, "wiring.Register(") || fileMentions(mainPath, `"`+wiringImport+`"`) {
		return nil
	}
	return []string{cfg.Output.Main + " imports a wiring package other than " + wiringImport +
		" - it is generated once, so point its import at output.wiring and delete the directory the key used to name"}
}

func fileHasPrefix(path, prefix string) bool {
	body, err := os.ReadFile(path)
	return err == nil && strings.HasPrefix(string(body), prefix)
}

func fileMentions(path, needle string) bool {
	body, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(body), needle)
}
