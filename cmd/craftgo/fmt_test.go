package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestRunFmtLeavesFilesWithErrorsAlone checks that fmt formats the clean file,
// leaves the one with an analyser error untouched and fails.
func TestRunFmtLeavesFilesWithErrorsAlone(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "design/craftgo.design.yaml", "")
	mustWrite(t, dir, "design/ok.craftgo", "package d\n\ntype A {  x   string }\n")
	bad := "package d\n\ntype B {  y   Missing }\n"
	mustWrite(t, dir, "design/bad.craftgo", bad)
	err := runFmt([]string{"-w", filepath.Join(dir, "design")})
	if err == nil || !strings.Contains(err.Error(), "1 file(s) left unformatted") {
		t.Fatalf("err = %v, want the unformatted-file error", err)
	}
	okOut, _ := os.ReadFile(filepath.Join(dir, "design", "ok.craftgo"))
	if !strings.Contains(string(okOut), "\tx string\n") {
		t.Errorf("clean file not formatted:\n%s", okOut)
	}
	badOut, _ := os.ReadFile(filepath.Join(dir, "design", "bad.craftgo"))
	if string(badOut) != bad {
		t.Errorf("file with errors was rewritten:\n%s", badOut)
	}
}

// brokenDesign leaves a decorator open, so the parser reads the next fields as
// its arguments.
const brokenDesign = "package app\n\ntype D {\n    id      string @minLength(1\n    name    string\n    garbage\n}\n"

// fmt refuses a file with a parse or semantic error whatever path names it,
// and leaves the file untouched.
func TestRunFmtRefusesErrorsUnderAnyPath(t *testing.T) {
	semanticError := "package app\n\ntype B {  y   Missing }\n"
	for _, c := range []struct {
		name, src string
		args      []string
	}{
		{"relative file", brokenDesign, []string{"design/app/app.craftgo"}},
		{"relative directory", brokenDesign, []string{"design"}},
		{"no path", brokenDesign, nil},
		{"semantic error, relative file", semanticError, []string{"design/app/app.craftgo"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			mustWrite(t, dir, "design/craftgo.design.yaml", "")
			mustWrite(t, dir, "design/app/app.craftgo", c.src)
			t.Chdir(dir)
			err := runFmt(c.args)
			if err == nil || !strings.Contains(err.Error(), "1 file(s) left unformatted") {
				t.Fatalf("err = %v, want the unformatted-file error", err)
			}
			got, _ := os.ReadFile(filepath.Join(dir, "design", "app", "app.craftgo"))
			if string(got) != c.src {
				t.Errorf("file with errors was rewritten:\n%s", got)
			}
		})
	}
}

// fmt finds a file in its project's analysis by identity, so a path spelled
// in another case or through a symbolic link still meets the file's errors.
func TestRunFmtRefusesErrorsUnderAnotherSpelling(t *testing.T) {
	semanticError := "package app\n\ntype B {  y   Missing }\n"
	for _, c := range []struct {
		name  string
		setup func(t *testing.T, dir string)
		arg   string
	}{
		{"another case", func(t *testing.T, dir string) {
			if _, err := os.Stat(filepath.Join(dir, "DESIGN", "APP", "APP.CRAFTGO")); err != nil {
				t.Skip("the file system tells cases apart")
			}
		}, "DESIGN/APP/APP.CRAFTGO"},
		{"linked design folder", func(t *testing.T, dir string) {
			if err := os.Symlink(filepath.Join(dir, "design"), filepath.Join(dir, "link")); err != nil {
				t.Skipf("no symbolic links here: %v", err)
			}
		}, "link/app/app.craftgo"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			mustWrite(t, dir, "design/craftgo.design.yaml", "")
			mustWrite(t, dir, "design/app/app.craftgo", semanticError)
			c.setup(t, dir)
			t.Chdir(dir)
			err := runFmt([]string{c.arg})
			if err == nil || !strings.Contains(err.Error(), "1 file(s) left unformatted") {
				t.Fatalf("err = %v, want the unformatted-file error", err)
			}
			if got, _ := os.ReadFile(filepath.Join(dir, "design", "app", "app.craftgo")); string(got) != semanticError {
				t.Errorf("file with errors was rewritten:\n%s", got)
			}
		})
	}
}

// fmt reads its flags and path like gen and init: flags first, one path at
// most, and `-h` asks for help.
func TestRunFmtArguments(t *testing.T) {
	unformatted := "package app\n\ntype A {  x   string }\n"
	for _, c := range []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"flag after the path", []string{"design", "-l"}, `fmt: flag "-l" follows the path - flags go before it`},
		{"two paths", []string{"design", "design/app.craftgo"}, "fmt: too many positional arguments (got 2, want at most 1)"},
		{"unknown flag", []string{"-x"}, "fmt: flag provided but not defined: -x"},
		{"help", []string{"-h"}, errHelpRequested.Error()},
		{"list", []string{"-l", "design"}, errFilesDiffer.Error()},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			mustWrite(t, dir, "design/craftgo.design.yaml", "")
			mustWrite(t, dir, "design/app.craftgo", unformatted)
			t.Chdir(dir)
			if err := runFmt(c.args); err == nil || err.Error() != c.wantErr {
				t.Fatalf("err = %v, want %q", err, c.wantErr)
			}
			if got, _ := os.ReadFile(filepath.Join(dir, "design", "app.craftgo")); string(got) != unformatted {
				t.Errorf("file was rewritten:\n%s", got)
			}
		})
	}
}

// fmt analyses a file beside the design folder on its own, so its semantic
// error blocks it.
func TestRunFmtChecksAFileOutsideTheDesignRootAlone(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "design/craftgo.design.yaml", "")
	mustWrite(t, dir, "design/app.craftgo", "package app\n\ntype A {\n\tx string\n}\n")
	stray := "package stray\n\ntype B {  y   Missing }\n"
	mustWrite(t, dir, "stray.craftgo", stray)
	t.Chdir(dir)
	err := runFmt([]string{"stray.craftgo"})
	if err == nil || !strings.Contains(err.Error(), "1 file(s) left unformatted") {
		t.Fatalf("err = %v, want the unformatted-file error", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "stray.craftgo")); string(got) != stray {
		t.Errorf("file with errors was rewritten:\n%s", got)
	}
}

// fmt honours the diagnostics of the formatter itself, here the parse errors of
// a file beside the design folder that the project's analysis does not cover.
func TestRunFmtRefusesWhatFormatReports(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "design/craftgo.design.yaml", "")
	mustWrite(t, dir, "design/app.craftgo", "package app\n\ntype A {\n\tx string\n}\n")
	mustWrite(t, dir, "stray.craftgo", brokenDesign)
	t.Chdir(dir)
	err := runFmt([]string{"stray.craftgo"})
	if err == nil || !strings.Contains(err.Error(), "1 file(s) left unformatted") {
		t.Fatalf("err = %v, want the unformatted-file error", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "stray.craftgo")); string(got) != brokenDesign {
		t.Errorf("file with errors was rewritten:\n%s", got)
	}
}

// unformattedText is a clean design file fmt rewrites; formattedText is its
// canonical form.
const (
	unformattedText = "package app\n\ntype A {  x   string }\n"
	formattedText   = "package app\n\ntype A {\n\tx string\n}\n"
)

// An explicit -w=false prints the formatted text, as gofmt does, and writes
// nothing; with -l it lists alone.
func TestRunFmtWithWriteOffPrintsTheResult(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "design/craftgo.design.yaml", "")
	mustWrite(t, dir, "design/app.craftgo", unformattedText)
	t.Chdir(dir)
	var err error
	stdout, _ := captureOutput(t, func() { err = runFmt([]string{"-w=false", "design/app.craftgo"}) })
	if err != nil || stdout != formattedText {
		t.Errorf("fmt -w=false: err = %v, stdout = %q; want the formatted text", err, stdout)
	}
	stdout, _ = captureOutput(t, func() { err = runFmt([]string{"-l", "-w=false", "design"}) })
	if err != errFilesDiffer || stdout != filepath.Join("design", "app.craftgo")+"\n" {
		t.Errorf("fmt -l -w=false: err = %v, stdout = %q; want the file listed", err, stdout)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "design", "app.craftgo")); string(got) != unformattedText {
		t.Errorf("the file was rewritten:\n%s", got)
	}
}

// A file fmt cannot write is reported and passed over: the files after it
// are still formatted, and the run fails at the end.
func TestRunFmtGoesOnPastAFileItCannotWrite(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "design/craftgo.design.yaml", "")
	for _, name := range []string{"a", "b", "c"} {
		mustWrite(t, dir, "design/"+name+".craftgo", strings.Replace(unformattedText, "A", strings.ToUpper(name)+"x", 1))
	}
	locked := filepath.Join(dir, "design", "b.craftgo")
	if err := os.Chmod(locked, 0o444); err != nil {
		t.Fatal(err)
	}
	if f, err := os.OpenFile(locked, os.O_WRONLY, 0); err == nil {
		f.Close()
		t.Skip("a read-only file is writable here (running as root?)")
	}
	var err error
	_, stderr := captureOutput(t, func() { err = runFmt([]string{filepath.Join(dir, "design")}) })
	if err == nil || err.Error() != "1 file(s) could not be read or written" {
		t.Fatalf("err = %v, want the unwritten file counted", err)
	}
	if !strings.Contains(stderr, "cannot write "+locked) {
		t.Errorf("stderr does not name the locked file:\n%s", stderr)
	}
	for name, formatted := range map[string]bool{"a": true, "b": false, "c": true} {
		got, _ := os.ReadFile(filepath.Join(dir, "design", name+".craftgo"))
		if strings.Contains(string(got), "\tx string\n") != formatted {
			t.Errorf("%s.craftgo formatted = %v, want %v:\n%s", name, !formatted, formatted, got)
		}
	}
}

// fmt replaces a file through a rename that keeps the file's mode.
func TestRunFmtKeepsTheFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows keeps no Unix permission bits")
	}
	dir := t.TempDir()
	mustWrite(t, dir, "design/craftgo.design.yaml", "")
	mustWrite(t, dir, "design/app.craftgo", unformattedText)
	path := filepath.Join(dir, "design", "app.craftgo")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runFmt([]string{path}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, %v; want 0600", info.Mode(), err)
	}
	if got, _ := os.ReadFile(path); string(got) != formattedText {
		t.Errorf("the file was not formatted:\n%s", got)
	}
}

// fmt with no path walks the working directory as a design walk does: it
// passes over hidden directories, vendor, node_modules and editor artefacts,
// and warns of a directory it cannot read and goes on.
func TestRunFmtWalksLikeTheDesignWalk(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "design/craftgo.design.yaml", "")
	mustWrite(t, dir, "design/users/u.craftgo", unformattedText)
	mustWrite(t, dir, "design/users/._u.craftgo", "\x00\x05\x16\x07Mac OS X")
	for _, rel := range []string{"node_modules/p/t.craftgo", "vendor/v/t.craftgo", ".claude/worktrees/w/t.craftgo"} {
		mustWrite(t, dir, rel, unformattedText)
	}
	if err := os.Symlink("user@host.12345:1700000000", filepath.Join(dir, "design", "users", ".#u.craftgo")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	locked := filepath.Join(dir, "pgdata")
	mustWrite(t, dir, "pgdata/base/x", "")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	_, readErr := os.ReadDir(locked)
	t.Chdir(dir)
	var err error
	stdout, stderr := captureOutput(t, func() { err = runFmt([]string{"-l"}) })
	if err != errFilesDiffer || stdout != filepath.Join("design", "users", "u.craftgo")+"\n" {
		t.Errorf("err = %v, stdout = %q; want the one design file listed", err, stdout)
	}
	if readErr != nil && !strings.Contains(stderr, "craftgo: warning: skipping pgdata: ") {
		t.Errorf("stderr does not warn of the unreadable directory:\n%s", stderr)
	}
}

// fmt -l on a design folder that is a link checks the files it leads to.
func TestRunFmtChecksALinkedDesignFolder(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "shared-design/craftgo.design.yaml", "")
	mustWrite(t, dir, "shared-design/users/u.craftgo", unformattedText)
	if err := os.Symlink(filepath.Join(dir, "shared-design"), filepath.Join(dir, "app-design")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	t.Chdir(dir)
	var err error
	stdout, _ := captureOutput(t, func() { err = runFmt([]string{"-l", "app-design"}) })
	if err != errFilesDiffer || stdout != filepath.Join("app-design", "users", "u.craftgo")+"\n" {
		t.Errorf("err = %v, stdout = %q; want the linked file listed", err, stdout)
	}
}

// fmt prints every path as the run reached it, a semantic error's included.
func TestRunFmtPrintsPathsAsReached(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "design/craftgo.design.yaml", "")
	mustWrite(t, dir, "design/m.craftgo", "package m\n\ntype A { a strin }\n")
	t.Chdir(dir)
	var err error
	_, stderr := captureOutput(t, func() { err = runFmt([]string{"-l", "design"}) })
	if err == nil {
		t.Fatal("want the file with an error refused")
	}
	rel := filepath.Join("design", "m.craftgo")
	if !strings.Contains(stderr, rel+": not formatted") || !strings.Contains(stderr, "  "+rel+":3:") || strings.Contains(stderr, dir) {
		t.Errorf("stderr mixes spellings of %s:\n%s", rel, stderr)
	}
}

// A file named on its own is formatted whatever its extension, but for one
// that plainly holds something else.
func TestRunFmtRefusesAGoModFile(t *testing.T) {
	dir := t.TempDir()
	goMod := "module example.com/app\n\ngo 1.26\n"
	mustWrite(t, dir, "go.mod", goMod)
	mustWrite(t, dir, "notes.design", unformattedText)
	t.Chdir(dir)
	if err := runFmt([]string{"go.mod"}); err == nil || err.Error() != "go.mod is not a design file - fmt formats .craftgo and .cg files" {
		t.Errorf("fmt go.mod: err = %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "go.mod")); string(got) != goMod {
		t.Errorf("go.mod was rewritten:\n%s", got)
	}
	if err := runFmt([]string{"notes.design"}); err != nil {
		t.Fatalf("fmt notes.design: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "notes.design")); string(got) != formattedText {
		t.Errorf("notes.design was not formatted:\n%s", got)
	}
}
