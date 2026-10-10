package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/codegen/golang"
	"github.com/craftgodotdev/craftgo/internal/config"
)

// writeGenerated writes a file carrying the generated header at rel under dir and returns
// its path.
func writeGenerated(t *testing.T, dir, rel string) string {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(golang.GeneratedHeader+"\n\npackage x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// symlinkDir links name to target, skipping the test where links cannot be made.
func symlinkDir(t *testing.T, target, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, name); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
}

// An output directory that is a link into the project is swept like any other, a package the
// design dropped going from it; a link that leads out of the project is never followed.
func TestTheSweepFollowsLinksWithinTheProject(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	foreign := writeGenerated(t, outside, "types.go")
	if err := os.MkdirAll(filepath.Join(dir, "shared", "types"), 0o755); err != nil {
		t.Fatal(err)
	}
	symlinkDir(t, filepath.Join(dir, "shared", "types"), filepath.Join(dir, "internal", "types"))
	symlinkDir(t, outside, filepath.Join(dir, "internal", "transport", "elsewhere"))
	cfg := planConfig()
	if err := Generate(Inputs{Design: analyzeProject(t, planSrc...)}, cfg, dir); err != nil {
		t.Fatalf("generate: %v", err)
	}
	warehouse := filepath.Join(dir, "shared", "types", "warehouse", "types.go")
	if _, err := os.Stat(warehouse); err != nil {
		t.Fatalf("the first run wrote nothing through the link: %v", err)
	}
	if err := Generate(Inputs{Design: analyzeProject(t, planSrc[0])}, cfg, dir); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(warehouse)); !os.IsNotExist(err) {
		t.Errorf("the dropped package's types survived behind the link: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "shared", "types", "shop", "types.go")); err != nil {
		t.Errorf("the kept package's types went: %v", err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Errorf("the sweep followed a link out of the project: %v", err)
	}
	if info, err := os.Lstat(filepath.Join(dir, "internal", "types")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the linked output directory itself went: %v", err)
	}
}

// The event library a Go target left in its default directory goes once the target is turned
// off, emptied or moved; another design's library there, which imports other types, stays.
func TestAnEventTargetTurnedOffOrMovedLeavesNoLibrary(t *testing.T) {
	for name, targets := range map[string][]config.EventTarget{
		"off":   {{Lang: config.LangGo, Out: "-"}},
		"empty": {},
		"moved": {{Lang: config.LangGo, Out: "./pkg/events"}},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			proj := analyzeProject(t, ordersSrc)
			cfg := eventsConfig()
			if err := Generate(Inputs{Design: proj}, cfg, dir); err != nil {
				t.Fatalf("generate: %v", err)
			}
			library := filepath.Join(dir, "internal", "events", "orders", "events.go")
			if _, err := os.Stat(library); err != nil {
				t.Fatalf("the first run wrote no event library: %v", err)
			}
			other := filepath.Join(dir, "internal", "events", "billing", "events.go")
			body := golang.GeneratedHeader + "\n\npackage billing\n\nimport types \"example.com/other/internal/types/billing\"\n"
			if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(other, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg.Events.Targets = targets
			if err := Generate(Inputs{Design: proj}, cfg, dir); err != nil {
				t.Fatalf("generate: %v", err)
			}
			if _, err := os.Stat(filepath.Dir(library)); !os.IsNotExist(err) {
				t.Errorf("the stale event library survived: %v", err)
			}
			if _, err := os.Stat(other); err != nil {
				t.Errorf("another design's event library went: %v", err)
			}
			if name == "moved" {
				if _, err := os.Stat(filepath.Join(dir, "pkg", "events", "orders", "events.go")); err != nil {
					t.Errorf("the moved target wrote nothing: %v", err)
				}
			}
		})
	}
}

// On a file system that folds case, a directory renamed in case only keeps its old spelling on
// disk, where the run writes its files: the sweep reaches them under that spelling and keeps them.
func TestTheSweepKeepsWhatItWroteUnderAnotherCase(t *testing.T) {
	dir := t.TempDir()
	onDisk := writeGenerated(t, dir, "internal/transport/admin_svc/Admin/list.go")
	written := map[string]bool{filepath.Join(dir, "internal", "transport", "admin_svc", "admin", "list.go"): true}
	paths := owned(map[string][]string{filepath.Join(dir, "internal", "transport"): {golang.GeneratedHeader}}, dir)

	foldsCase := sameFile
	t.Cleanup(func() { sameFile = foldsCase })
	sameFile = func(a, b string) bool { return strings.EqualFold(a, b) }
	if err := prune(dir, paths, written); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(onDisk); err != nil {
		t.Fatalf("a file the run wrote, reached in another case, was swept: %v", err)
	}

	// Where case matters, the other spelling is another, stale file.
	sameFile = foldsCase
	for p := range written {
		if _, err := os.Stat(p); err == nil {
			t.Skip("this file system folds case")
		}
	}
	if err := prune(dir, paths, written); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(onDisk); !os.IsNotExist(err) {
		t.Errorf("a stale file in another case survived: %v", err)
	}
}
