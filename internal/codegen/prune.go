package codegen

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/codegen/docs"
	"github.com/craftgodotdev/craftgo/internal/codegen/golang"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/protodesign"
)

// generatedHeaders open the files craftgo rewrites on every run, Go and
// YAML. A gen-once scaffold opens with another line, so the sweep keeps it.
var generatedHeaders = []string{golang.GeneratedHeader, docs.GeneratedHeader}

// sweepDir is a directory the sweep walks and the headers that mark a file
// in it as regenerated: the plugins' in the pb directory, craftgo's elsewhere.
type sweepDir struct {
	path    string
	headers []string
}

// isGenerated reports whether the file at path opens with one of headers;
// a missing or unreadable file does not.
func isGenerated(path string, headers []string) bool {
	body, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, header := range headers {
		if strings.HasPrefix(string(body), header) {
			return true
		}
	}
	return false
}

// outputDirs are the directories the selected targets regenerate into.
func outputDirs(cfg *config.Config, projectRoot string, sel map[string]bool) []sweepDir {
	var dirs []sweepDir
	if sel[config.LangGo] {
		for _, dir := range golang.OutputDirs(cfg, projectRoot) {
			dirs = append(dirs, sweepDir{path: dir, headers: generatedHeaders})
		}
		if !cfg.Output.PBDisabled() {
			dirs = append(dirs, sweepDir{path: filepath.Join(projectRoot, cfg.Output.PB), headers: protodesign.PluginHeaders})
		}
	}
	dirs = append(dirs, eventOutputDirs(cfg, projectRoot, sel)...)
	if sel[TargetDocs] {
		if doc := docs.DocumentPath(cfg, projectRoot); doc != "" {
			dirs = append(dirs, sweepDir{path: filepath.Dir(doc), headers: generatedHeaders})
		}
	}
	return owned(dirs, projectRoot)
}

// eventOutputDirs is [outputDirs] for the event language targets alone.
func eventOutputDirs(cfg *config.Config, projectRoot string, sel map[string]bool) []sweepDir {
	var dirs []sweepDir
	for _, target := range LangTargets {
		if !sel[target.Lang] {
			continue
		}
		cfgTarget, ok := cfg.Events.TargetFor(target.Lang)
		if !ok || !cfgTarget.Enabled() {
			continue
		}
		dirs = append(dirs, sweepDir{path: filepath.Join(projectRoot, cfgTarget.Out), headers: generatedHeaders})
	}
	return owned(dirs, projectRoot)
}

// owned drops duplicates, the project root and its ancestors, and sorts the
// rest. The root may hold a sibling design's output, so it is never swept.
func owned(dirs []sweepDir, projectRoot string) []sweepDir {
	root := filepath.Clean(projectRoot)
	seen := map[string]bool{}
	out := make([]sweepDir, 0, len(dirs))
	for _, dir := range dirs {
		dir.path = filepath.Clean(dir.path)
		if dir.path == root || strings.HasPrefix(root, dir.path+string(filepath.Separator)) || seen[dir.path] {
			continue
		}
		seen[dir.path] = true
		out = append(out, dir)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

// regeneratedFiles is every file the project's targets produce, whatever
// `--target` selects: a target that does not run still owns its files.
func regeneratedFiles(in Inputs, cfg *config.Config, projectRoot string) map[string]bool {
	proj := in.Design
	files := golang.RegeneratedFiles(proj, in.Protos, cfg, projectRoot)
	for _, target := range LangTargets {
		cfgTarget, ok := cfg.Events.TargetFor(target.Lang)
		if !ok || !cfgTarget.Enabled() {
			continue
		}
		if target.Lang == config.LangGo {
			files = append(files, golang.RegeneratedEventFiles(proj, projectRoot, cfgTarget.Out)...)
		}
	}
	if doc := docs.RegeneratedFile(proj, cfg, projectRoot); doc != "" {
		files = append(files, doc)
	}
	set := make(map[string]bool, len(files))
	for _, file := range files {
		set[file] = true
	}
	return set
}

// prune deletes, under each of dirs, the generated files written does not
// name, then the directories that leaves empty.
func prune(dirs []sweepDir, written map[string]bool) error {
	for _, dir := range dirs {
		root := dir.path
		var emptied []string
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || written[path] || !isGenerated(path, dir.headers) {
				return nil
			}
			if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
				return rmErr
			}
			emptied = append(emptied, filepath.Dir(path))
			return nil
		})
		if err != nil {
			return err
		}
		pruneEmptyDirs(root, emptied)
	}
	return nil
}

// pruneEmptyDirs removes the emptied dirs, deepest first, and each parent
// that leaves empty, up to but not including root.
func pruneEmptyDirs(root string, dirs []string) {
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, dir := range dirs {
		for strings.HasPrefix(dir, root+string(filepath.Separator)) {
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) > 0 {
				break
			}
			if os.Remove(dir) != nil {
				break
			}
			dir = filepath.Dir(dir)
		}
	}
}
