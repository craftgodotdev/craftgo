package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// layout writes each slash path of files under a fresh directory, a path
// ending in / as a directory, and returns the directory with links resolved.
func layout(t *testing.T, files map[string]string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if strings.HasSuffix(rel, "/") {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, p, body)
	}
	return root
}

// symlink makes the link name to target, skipping the test where links
// cannot be made.
func symlink(t *testing.T, target, name string) {
	t.Helper()
	if err := os.Symlink(target, name); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
}

// The walk-up stops at the directory holding go.mod: a design an enclosing
// module holds is never picked up, while one of the same module is.
func TestFindStopsAtTheModuleBoundary(t *testing.T) {
	top := layout(t, map[string]string{
		"go.mod":                     "module example.com/top\n",
		"old-design/" + Filename:     "",
		"svc/internal/":              "",
		"inner/go.mod":               "module example.com/inner\n",
		"inner/api/spec/" + Filename: "",
		"inner/cmd/tool/":            "",
	})
	for _, start := range []string{filepath.Join(top, "inner", "cmd", "tool"), filepath.Join(top, "inner")} {
		_, _, _, err := Find(start)
		if err == nil || !strings.Contains(err.Error(), filepath.Join(top, "inner")+", which holds go.mod") {
			t.Fatalf("from %s: err = %v, want the walk to stop at inner/, which holds go.mod", start, err)
		}
	}
	_, projectRoot, design, err := Find(filepath.Join(top, "svc", "internal"))
	if err != nil {
		t.Fatalf("Find in the same module: %v", err)
	}
	if projectRoot != top || design != filepath.Join(top, "old-design") {
		t.Errorf("Find = %s, %s; want the module's own design", projectRoot, design)
	}
}

// The walk-up stops at a repository root, go.mod or not.
func TestFindStopsAtTheRepositoryRoot(t *testing.T) {
	top := layout(t, map[string]string{
		"design/" + Filename: "",
		"repo/.git/":         "",
		"repo/tools/x/":      "",
	})
	_, _, _, err := Find(filepath.Join(top, "repo", "tools", "x"))
	if err == nil || !strings.Contains(err.Error(), "is the root of a repository (.git)") {
		t.Fatalf("err = %v, want the walk to stop at the repository root", err)
	}
}

// A design folder linked into the project is probed like any subdirectory.
func TestFindProbesALinkedDesignFolder(t *testing.T) {
	top := layout(t, map[string]string{
		"shared-design/" + Filename: "",
		"app/go.mod":                "module example.com/app\n",
	})
	app := filepath.Join(top, "app")
	symlink(t, filepath.Join(top, "shared-design"), filepath.Join(app, "design"))
	_, projectRoot, design, err := Find(app)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if projectRoot != app || design != filepath.Join(app, "design") {
		t.Errorf("Find = %s, %s; want %s and its linked design", projectRoot, design, app)
	}
}

// A manifest link that leads nowhere is an error, not a missing manifest the
// walk-up passes by.
func TestFindRefusesADanglingManifestLink(t *testing.T) {
	top := layout(t, map[string]string{
		"design/" + Filename:       "",
		"svc/design/users/x.notes": "",
	})
	svc := filepath.Join(top, "svc")
	symlink(t, filepath.Join(top, "shared", Filename), filepath.Join(svc, "design", Filename))
	for name, find := range map[string]func() error{
		"walk-up": func() error { _, _, _, err := Find(svc); return err },
		"-f":      func() error { _, _, _, err := FindAt(filepath.Join(svc, "design")); return err },
	} {
		if err := find(); err == nil || !strings.Contains(err.Error(), "is a symbolic link to") {
			t.Errorf("%s: err = %v, want the dangling link named", name, err)
		}
	}
}

// Probing skips hidden directories, vendor and node_modules, as a design walk
// does.
func TestFindSkipsHiddenAndVendoredFolders(t *testing.T) {
	top := layout(t, map[string]string{
		"go.mod":                       "module example.com/top\n",
		".design/" + Filename:          "",
		"vendor/" + Filename:           "",
		"node_modules/" + Filename:     "",
		"svc/node_modules/" + Filename: "",
	})
	if _, _, design, err := Find(filepath.Join(top, "svc")); err == nil {
		t.Errorf("Find picked %s", design)
	}
}
