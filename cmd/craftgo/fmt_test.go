package main

import (
	"os"
	"path/filepath"
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
