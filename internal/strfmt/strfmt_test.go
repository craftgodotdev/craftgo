package strfmt

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestSpecsAreWellFormed checks that every spec has a unique name, a label and
// exactly one check, with Valid set for a condition.
func TestSpecsAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range All {
		if s.Name == "" || s.Label == "" {
			t.Errorf("%+v: name and label are required", s)
		}
		if seen[s.Name] {
			t.Errorf("%s: listed twice", s.Name)
		}
		seen[s.Name] = true
		switch {
		case s.Cond != "" && s.Pattern != "":
			t.Errorf("%s: both a condition and a pattern", s.Name)
		case s.Cond == "" && s.Pattern == "":
			t.Errorf("%s: no check", s.Name)
		case s.Cond != "" && strings.Count(s.Cond, "%s") != 1:
			t.Errorf("%s: condition needs exactly one %%s, got %q", s.Name, s.Cond)
		case s.Cond != "" && s.valid == nil:
			t.Errorf("%s: a condition needs its valid function", s.Name)
		}
	}
	if got := OpenAPIFormat("datetime"); got != "date-time" {
		t.Errorf("OpenAPIFormat(datetime) = %q, want date-time", got)
	}
	if got := OpenAPIFormat("email"); got != "email" {
		t.Errorf("OpenAPIFormat(email) = %q, want the name itself", got)
	}
}

// formatSamples are values each format's check accepts or refuses.
var formatSamples = []string{
	"", "x", "a@b.co", "Ann <ann@example.com>", "not an email", "https://example.com/a?b=1",
	"http://x", "ftp://x.io", "example.com", "mailto:a@b.co", "urn:isbn:0451450523",
	"123e4567-e89b-12d3-a456-426614174000", "123e4567e89b12d3a456426614174000",
	"2024-01-02T03:04:05Z", "2024-01-02T03:04:05+07:00", "2024-01-02", "2024-13-02",
	"15:04:05", "25:00:00", "+1 (555) 123-4567", "12345", "10.0.0.1", "256.0.0.1", "::1",
	"::ffff:10.0.0.1", "fe80::1", "10.0.0.0/8", "fe80::/10", "10.0.0.1/33",
	"01:23:45:67:89:ab", "01-23-45-67-89-AB", "0123.4567.89ab", "4111111111111111",
	"aGVsbG8=", "aGVsbG8", "aGVsbG8-_w==", "#fff", "#FFFFFF", "fff0", `{"a": 1}`, "[1, 2]",
	"[1,", "null",
}

// Valid accepts exactly what the code generated from Cond or Pattern
// accepts: a program built from the generated checks judges every sample.
func TestValidMatchesTheGeneratedCheck(t *testing.T) {
	imports := []string{"fmt", "regexp"}
	var checks strings.Builder
	for i, s := range All {
		imports = append(imports, s.Imports...)
		cond := fmt.Sprintf("!regexp.MustCompile(%q).MatchString(v)", s.Pattern)
		if s.Cond != "" {
			cond = fmt.Sprintf(s.Cond, "v")
		}
		fmt.Fprintf(&checks, "\tfunc(v string) bool {\n\t\tif %s {\n\t\t\treturn false\n\t\t}\n\t\treturn true\n\t}, // %d %s\n", cond, i, s.Name)
	}
	slices.Sort(imports)
	var quoted []string
	for _, imp := range slices.Compact(imports) {
		quoted = append(quoted, strconv.Quote(imp))
	}
	samples := make([]string, len(formatSamples))
	for i, v := range formatSamples {
		samples[i] = strconv.Quote(v)
	}
	program := fmt.Sprintf(`package main

import (
	%s
)

var checks = []func(string) bool{
%s}

var samples = []string{%s}

func main() {
	for i, check := range checks {
		for j, v := range samples {
			fmt.Println(i, j, check(v))
		}
	}
}
`, strings.Join(quoted, "\n\t"), checks.String(), strings.Join(samples, ", "))

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module formatprobe\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	run := exec.Command("go", "run", ".")
	run.Dir = dir
	run.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=")
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("go run: %v\n%s\n%s", err, out, program)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != len(All)*len(formatSamples) {
		t.Fatalf("the program judged %d pairs, want %d:\n%s", len(lines), len(All)*len(formatSamples), out)
	}
	for _, line := range lines {
		var i, j int
		var want bool
		if _, err := fmt.Sscan(line, &i, &j, &want); err != nil {
			t.Fatalf("line %q: %v", line, err)
		}
		if got := All[i].Valid(formatSamples[j]); got != want {
			t.Errorf("%s: Valid(%q) = %v, the generated check says %v", All[i].Name, formatSamples[j], got, want)
		}
	}
}
