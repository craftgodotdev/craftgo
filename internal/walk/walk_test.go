package walk

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// tree writes each slash path of files, with empty content, under a new
// directory and returns it with links resolved.
func tree(t *testing.T, files ...string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		p := filepath.Join(dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// link makes the symbolic link name pointing at target, skipping the test
// where links cannot be made.
func link(t *testing.T, target, name string) {
	t.Helper()
	if err := os.Symlink(target, name); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
}

// collect walks root with o and returns the slash paths, relative to root,
// fn received.
func collect(t *testing.T, root string, o Options) []string {
	t.Helper()
	var got []string
	err := Files(root, o, func(path string) error {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		got = append(got, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	return got
}

// Files reaches files in lexical order, a directory's files where its name
// sorts among its siblings.
func TestFilesVisitsInLexicalOrder(t *testing.T) {
	root := tree(t, "c.txt", "a.txt", "b/z.txt", "b/a/y.txt")
	want := []string{"a.txt", "b/a/y.txt", "b/z.txt", "c.txt"}
	if got := collect(t, root, Options{}); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// A link to a directory is entered under the link's own name; a directory
// two paths reach is entered once, so a link cycle ends; a link to nothing is
// passed over.
func TestFilesFollowsLinksOnce(t *testing.T) {
	shared := tree(t, "pkg/p.txt")
	root := tree(t, "own/o.txt")
	link(t, filepath.Join(shared, "pkg"), filepath.Join(root, "linked"))
	link(t, root, filepath.Join(root, "own", "loop"))
	link(t, filepath.Join(root, "missing"), filepath.Join(root, "dangling.txt"))
	want := []string{"linked/p.txt", "own/o.txt"}
	if got := collect(t, root, Options{}); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// A walk rooted at a link to a directory walks the directory.
func TestFilesEntersALinkedRoot(t *testing.T) {
	shared := tree(t, "u/u.txt")
	root := tree(t)
	link(t, shared, filepath.Join(root, "design"))
	want := []string{"u/u.txt"}
	if got := collect(t, filepath.Join(root, "design"), Options{}); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// Within keeps the walk to one directory: a link out of it, or a root
// outside it, is not entered.
func TestFilesStaysWithin(t *testing.T) {
	outside := tree(t, "x.txt")
	project := tree(t, "out/a.txt", "elsewhere/b.txt")
	link(t, outside, filepath.Join(project, "out", "away"))
	link(t, filepath.Join(project, "elsewhere"), filepath.Join(project, "out", "near"))
	want := []string{"a.txt", "near/b.txt"}
	if got := collect(t, filepath.Join(project, "out"), Options{Within: project}); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if got := collect(t, outside, Options{Within: project}); got != nil {
		t.Errorf("a root outside the bound was walked: %v", got)
	}
}

// OnError decides on a path that cannot be read: nil passes it over, an
// error ends the walk; without OnError the walk ends.
func TestFilesHandsUnreadablePathsToOnError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	var seen string
	err := Files(missing, Options{OnError: func(path string, err error) error {
		seen = path
		return nil
	}}, func(string) error { return nil })
	if err != nil || seen != missing {
		t.Errorf("err = %v, OnError saw %q; want nil and %q", err, seen, missing)
	}
	stop := errors.New("stop")
	if err := Files(missing, Options{OnError: func(string, error) error { return stop }}, func(string) error { return nil }); err != stop {
		t.Errorf("err = %v, want OnError's", err)
	}
	if err := Files(missing, Options{}, func(string) error { return nil }); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want the stat error", err)
	}
}

// A design walk passes over hidden directories, vendor, node_modules, and
// files whose name starts with . or _.
func TestDesignSkips(t *testing.T) {
	root := tree(t,
		"users/u.craftgo",
		"users/.#u.craftgo",
		"users/._u.craftgo",
		"users/_draft.craftgo",
		".git/x.craftgo",
		"vendor/v.craftgo",
		"node_modules/n.craftgo",
	)
	want := []string{"users/u.craftgo"}
	if got := collect(t, root, Design(nil)); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
