package genfile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// read returns the content of path, or "<missing>".
func read(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "<missing>"
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// entries lists the names in dir.
func entries(t *testing.T, dir string) []string {
	t.Helper()
	list, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range list {
		out = append(out, e.Name())
	}
	return out
}

// Collect holds every write in memory: a regenerated file's last write wins,
// a gen-once file's first does, and nothing reaches the disk; a render that
// fails returns no file.
func TestCollectHoldsTheWrites(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a", "a.go"), filepath.Join(dir, "b.go")
	files, err := Collect(func() error {
		for _, step := range []func() error{
			func() error { return Write(a, []byte("first")) },
			func() error { return WriteOnce(b, func() ([]byte, error) { return []byte("once"), nil }) },
			func() error { return Write(a, []byte("second")) },
			func() error {
				return WriteOnce(b, func() ([]byte, error) { return nil, errors.New("rendered twice") })
			},
		} {
			if err := step(); err != nil {
				return err
			}
		}
		if !Exists(a) || !Exists(b) {
			t.Error("Exists misses a file the running Collect holds")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []File{{Path: a, Body: []byte("second")}, {Path: b, Body: []byte("once"), Once: true}}
	if len(files) != len(want) {
		t.Fatalf("files = %v, want %v", files, want)
	}
	for i := range want {
		if files[i].Path != want[i].Path || string(files[i].Body) != string(want[i].Body) || files[i].Once != want[i].Once {
			t.Errorf("files[%d] = %+v, want %+v", i, files[i], want[i])
		}
	}
	if got := entries(t, dir); got != nil {
		t.Errorf("Collect wrote to disk: %v", got)
	}
	failed := errors.New("render failed")
	if files, err := Collect(func() error { _ = Write(a, []byte("x")); return failed }); err != failed || files != nil {
		t.Errorf("Collect = %v, %v; want no file and the render's error", files, err)
	}
	if Exists(a) {
		t.Error("Exists still sees a file of a finished Collect")
	}
}

// Commit writes new files in new directories, replaces an existing file
// keeping its mode, skips an existing gen-once file, and leaves no temporary
// behind.
func TestCommitWritesEveryFile(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "keep.go")
	if err := os.WriteFile(existing, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	stub := filepath.Join(dir, "stub.go")
	if err := os.WriteFile(stub, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(dir, "x", "y", "new.go")
	err := Commit([]File{
		{Path: existing, Body: []byte("new")},
		{Path: stub, Body: []byte("scaffold"), Once: true},
		{Path: fresh, Body: []byte("fresh")},
		{Path: filepath.Join(dir, "x", "once.go"), Body: []byte("once"), Once: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{existing: "new", stub: "mine", fresh: "fresh", filepath.Join(dir, "x", "once.go"): "once"} {
		if got := read(t, path); got != want {
			t.Errorf("%s = %q, want %q", filepath.Base(path), got, want)
		}
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(existing); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("the replaced file lost its mode: %v, %v", info.Mode(), err)
		}
	}
	for _, d := range []string{dir, filepath.Join(dir, "x"), filepath.Join(dir, "x", "y")} {
		for _, name := range entries(t, d) {
			if strings.HasSuffix(name, ".tmp") {
				t.Errorf("a temporary is left in %s: %s", d, name)
			}
		}
	}
}

// A file Commit cannot write stops it before any file is replaced: the other
// files keep their content, and the directories made for them go.
func TestCommitLeavesTheTreeAsItWasOnFailure(t *testing.T) {
	dir := t.TempDir()
	kept := filepath.Join(dir, "kept.go")
	if err := os.WriteFile(kept, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(dir, "blocked.go")
	if err := os.Mkdir(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	err := Commit([]File{
		{Path: kept, Body: []byte("new")},
		{Path: filepath.Join(dir, "made", "new.go"), Body: []byte("new")},
		{Path: blocked, Body: []byte("new")},
	})
	if err == nil || !strings.Contains(err.Error(), "cannot write "+blocked+": a directory is in the way") {
		t.Fatalf("err = %v, want the blocked file named", err)
	}
	if got := read(t, kept); got != "old" {
		t.Errorf("kept.go = %q, want it untouched", got)
	}
	if got := entries(t, dir); strings.Join(got, ",") != "blocked.go,kept.go" {
		t.Errorf("the directory holds %v, want only what was there", got)
	}
}

// A file the user cannot write stops the commit as an in-place write would,
// though a rename could replace it.
func TestCommitRefusesAReadOnlyFile(t *testing.T) {
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked.go")
	if err := os.WriteFile(locked, []byte("old"), 0o444); err != nil {
		t.Fatal(err)
	}
	if f, err := os.OpenFile(locked, os.O_WRONLY, 0); err == nil {
		f.Close()
		t.Skip("a read-only file is writable here (running as root?)")
	}
	err := Commit([]File{{Path: locked, Body: []byte("new")}})
	if err == nil || !strings.Contains(err.Error(), "cannot write "+locked) {
		t.Fatalf("err = %v, want the read-only file named", err)
	}
	if got := read(t, locked); got != "old" {
		t.Errorf("locked.go = %q, want it untouched", got)
	}
}

// A link at a file's path is written through, and stays a link.
func TestCommitWritesThroughALink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "shared", "doc.yaml")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "doc.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	if err := Commit([]File{{Path: link, Body: []byte("new")}}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the link was replaced: %v, %v", info, err)
	}
	if got := read(t, target); got != "new" {
		t.Errorf("the link's target = %q, want the new content", got)
	}
}
