package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// mustWrite writes root/rel, creating its directories.
func mustWrite(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// exists reports whether the path joined from parts exists.
func exists(t *testing.T, parts ...string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(parts...))
	return err == nil
}

// treeOf maps each generated file under root, by relative slash path, to its
// contents.
func treeOf(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) == ".craftgo" || filepath.Base(path) == "craftgo.design.yaml" {
			return err
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		out[filepath.ToSlash(rel)] = string(body)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// genProject runs gen over dir/design into dir.
func genProject(t *testing.T, dir string) {
	t.Helper()
	if err := runGen([]string{"-f", filepath.Join(dir, "design"), "-c", dir}); err != nil {
		t.Fatalf("runGen %s: %v", dir, err)
	}
}

// goIn runs the go command with args in dir, under the workspace dir/go.work,
// and returns its combined output.
func goIn(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK="+filepath.Join(dir, "go.work"), "GOFLAGS=")
	return cmd.CombinedOutput()
}

// goCheck builds and vets the generated project inside its workspace.
func goCheck(t *testing.T, dir string) {
	t.Helper()
	for _, verb := range []string{"build", "vet"} {
		if out, err := goIn(dir, verb, "./..."); err != nil {
			t.Fatalf("go %s: %v\n%s", verb, err, out)
		}
	}
}

// writeWorkspace writes dir/go.work over dir, the repository at root and its
// nested modules, so a generated project resolves craftgo from this tree.
func writeWorkspace(t *testing.T, dir, root, goVersion string) {
	t.Helper()
	uses := []string{".", root}
	for _, m := range repoModules {
		uses = append(uses, filepath.Join(root, filepath.FromSlash(m)))
	}
	mustWrite(t, dir, "go.work", "go "+goVersion+"\n\nuse (\n\t"+strings.Join(uses, "\n\t")+"\n)\n")
}

// repoModules are the nested modules of this repo a generated project's
// workspace must use besides the root module.
var repoModules = []string{"pkg/events", "pkg/wire"}

// repoRoot walks up to the directory holding go.mod and every module in
// repoModules.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		_, mod := os.Stat(filepath.Join(dir, "go.mod"))
		nested := true
		for _, m := range repoModules {
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(m), "go.mod")); err != nil {
				nested = false
				break
			}
		}
		if mod == nil && nested {
			real, err := filepath.EvalSymlinks(dir)
			if err != nil {
				t.Fatal(err)
			}
			return real
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no craftgo module root above %s", dir)
		}
		dir = parent
	}
}

// goDirective returns the go version of the root go.mod.
func goDirective(t *testing.T, root string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^go (\S+)$`).FindStringSubmatch(string(body))
	if m == nil {
		t.Fatalf("no go directive in %s/go.mod", root)
	}
	return strings.TrimSpace(m[1])
}
