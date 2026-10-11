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
// exactly one check, a Func one of checks.go's.
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
		case s.Func != "" && s.Pattern != "":
			t.Errorf("%s: both a function and a pattern", s.Name)
		case s.Func == "" && s.Pattern == "":
			t.Errorf("%s: no check", s.Name)
		case s.Func != "" && checks[s.Func] == nil:
			t.Errorf("%s: %s is not one of the checks", s.Name, s.Func)
		case s.Func != "" && (len(s.Helpers()) == 0 || !slices.ContainsFunc(s.Helpers(), func(h Helper) bool { return h.Name == s.Func })):
			t.Errorf("%s: checks.go declares no %s", s.Name, s.Func)
		}
	}
	if got := OpenAPIFormat("datetime"); got != "date-time" {
		t.Errorf("OpenAPIFormat(datetime) = %q, want date-time", got)
	}
	if got := OpenAPIFormat("email"); got != "email" {
		t.Errorf("OpenAPIFormat(email) = %q, want the name itself", got)
	}
}

// A check's helpers are its function and the functions it calls, in checks.go
// order, each with its doc comment; its imports are theirs.
func TestHelpersCarryWhatTheCheckCalls(t *testing.T) {
	dt, _ := Lookup("datetime")
	var names []string
	for _, h := range dt.Helpers() {
		names = append(names, h.Name)
		if !strings.HasPrefix(h.Code, "// "+h.Name+" ") || !strings.Contains(h.Code, "func "+h.Name+"(") {
			t.Errorf("%s: the code is not its documented declaration:\n%s", h.Name, h.Code)
		}
	}
	if want := []string{"_isDateTime", "_isDate", "_isTime", "_twoDigits"}; !slices.Equal(names, want) {
		t.Errorf("datetime helpers = %v, want %v", names, want)
	}
	if !slices.Equal(dt.Imports, []string{"time"}) {
		t.Errorf("datetime imports = %v, want [time]", dt.Imports)
	}
	if uuid, _ := Lookup("uuid"); uuid.Helpers() != nil || uuid.Imports != nil {
		t.Errorf("a pattern calls no helper and imports nothing, got %v and %v", uuid.Helpers(), uuid.Imports)
	}
}

// Each format follows its RFC: what it accepts and what it refuses.
func TestFormatsFollowTheirRFCs(t *testing.T) {
	for _, c := range []struct {
		format        string
		valid, refuse []string
	}{
		{"email",
			[]string{"a@b.co", "first.last+tag@example.com", "A@B.CO", `"john doe"@example.com`, "a@[127.0.0.1]"},
			[]string{`"admin@victim.com" <attacker@evil.com>`, "=?utf-8?b?YWRtaW5AdmljdGltLmNvbQ==?= <attacker@evil.com>",
				"attacker@evil.com (admin@victim.com)", "<attacker@evil.com>", " a@b.co ", "a@b.co ", "Ann <ann@example.com>",
				"not an email", "", "a@", "@b.co", "a..b@c.co"}},
		{"url",
			[]string{"https://example.com/a?b=1", "http://x", "http://user:pw@host:8080/p#f", "HTTPS://EXAMPLE.COM", "http://[::1]:80/"},
			[]string{"http://", "http:", "http:foo", "http:///etc/passwd", "https://:443", "http://?q=1", "http://#frag", "http://@",
				"ftp://x.io", "example.com", "//x.io", "http://a.co/a b", "http://a.co/<x>"}},
		{"uri",
			[]string{"mailto:a@b.co", "urn:isbn:0451450523", "https://example.com/a%20b?c=d#e", "a:b", "file:///etc/hosts"},
			[]string{"http://a.co/a b", "http://a.co/<script>", "http://a.co/{x}", "http://a.co/x|y", "http://a.co/x^y",
				`http://a.co/"q"`, "http://a.co/x`y", `http://a.co/\x`, "http://a.co/%zz", "http://a.co/%4", "http://a.co/#a#b",
				"//x.io/a", "relative/path", "http://a.co/é"}},
		{"datetime",
			[]string{"2024-01-02T03:04:05Z", "2024-01-02T03:04:05+07:00", "2024-01-02t03:04:05z", "2024-01-02T03:04:05.123456789-23:59",
				"2016-12-31T23:59:60Z", "2016-12-31T15:59:60-08:00", "2024-02-29T00:00:00Z"},
			[]string{"2024-01-02T03:04:05+24:00", "2024-01-02T03:04:05+01:60", "2024-01-02T03:04:05,5Z", "2024-01-02T3:04:05Z",
				"2024-01-02T03:04:05", "2024-01-02 03:04:05Z", "2023-02-29T00:00:00Z", "2016-12-31T23:58:60Z", "2016-12-31T22:59:60Z",
				"2016-12-31T23:59:61Z", "2024-01-02T03:04:05.Z", "2024-01-02T03:04:05+0700", "2024-01-02T24:00:00Z"}},
		{"time",
			[]string{"15:04:05Z", "15:04:05+07:00", "15:04:05.123Z", "23:59:60Z", "15:59:60-08:00", "00:00:00z", "23:29:60+23:30"},
			[]string{"15:04:05", "5:04:05Z", "15:04:05,5Z", "24:00:00Z", "15:60:00Z", "15:04:05+24:00", "15:04:05+01:60",
				"22:59:60Z", "15:04Z", "15:04:05 Z", "23:59:60+01:00"}},
		{"date",
			[]string{"2024-01-02", "2024-02-29"},
			[]string{"2024-13-02", "2023-02-29", "2024-1-02", "20240102", "2024-01-02T00:00:00Z"}},
		{"ipv4",
			[]string{"10.0.0.1", "0.0.0.0", "255.255.255.255"},
			[]string{"::ffff:1.2.3.4", "::ffff:0102:0304", "0:0:0:0:0:ffff:1.2.3.4", "256.0.0.1", "1.2.3.04", "1.2.3", "::1"}},
		{"ipv6",
			[]string{"::1", "fe80::1", "::ffff:1.2.3.4", "::ffff:0102:0304", "2001:db8::8a2e:370:7334"},
			[]string{"fe80::1%eth0", "10.0.0.1", "::g", "1::2::3"}},
		{"base64url",
			[]string{"aGVsbG8", "aGVsbG8=", "aGk", "aGk=", "_-8", "eyJhbGciOiJIUzI1NiJ9"},
			[]string{"aGk==", "aG=", "a", "aGVs+bG8", "aGVs/bG8"}},
		{"phone",
			[]string{"+1 (555) 123-4567", "123456", "+12345678901234567890", "(555) 1234"},
			[]string{"      ", "------", "+()-()-", "12345", "+1 555 abc 4567", "123456789012345678901"}},
	} {
		s, ok := Lookup(c.format)
		if !ok {
			t.Fatalf("no format %s", c.format)
		}
		for _, v := range c.valid {
			if !s.Valid(v) {
				t.Errorf("%s refuses %q", c.format, v)
			}
		}
		for _, v := range c.refuse {
			if s.Valid(v) {
				t.Errorf("%s accepts %q", c.format, v)
			}
		}
	}
}

// formatSamples are values each format's check accepts or refuses.
var formatSamples = []string{
	"", "x", "a@b.co", "Ann <ann@example.com>", "not an email", "https://example.com/a?b=1",
	"http://x", "ftp://x.io", "example.com", "mailto:a@b.co", "urn:isbn:0451450523", "http://a.co/a b",
	"https://:443", "http:foo", "http://a.co/%zz",
	"123e4567-e89b-12d3-a456-426614174000", "123e4567e89b12d3a456426614174000",
	"2024-01-02T03:04:05Z", "2024-01-02T03:04:05+07:00", "2024-01-02t03:04:05z", "2024-01-02T03:04:05+24:00",
	"2016-12-31T23:59:60Z", "2024-01-02", "2024-13-02",
	"15:04:05", "15:04:05Z", "25:00:00", "+1 (555) 123-4567", "12345", "------", "10.0.0.1", "256.0.0.1", "::1",
	"::ffff:10.0.0.1", "fe80::1", "fe80::1%eth0", "10.0.0.0/8", "fe80::/10", "10.0.0.1/33",
	"01:23:45:67:89:ab", "01-23-45-67-89-AB", "0123.4567.89ab", "4111111111111111",
	"aGVsbG8=", "aGVsbG8", "aGVsbG8-_w==", "aGk", "#fff", "#FFFFFF", "fff0", `{"a": 1}`, "[1, 2]",
	"[1,", "null",
}

// Valid accepts exactly what the code generated from Func or Pattern
// accepts: a program built from the generated checks, and the functions they
// call, judges every sample.
func TestValidMatchesTheGeneratedCheck(t *testing.T) {
	imports := []string{"fmt", "regexp"}
	var conds, funcs strings.Builder
	var helpers []Helper
	for i, s := range All {
		imports = append(imports, s.Imports...)
		cond := fmt.Sprintf("!regexp.MustCompile(%q).MatchString(v)", s.Pattern)
		if s.Func != "" {
			cond = "!" + s.Func + "(v)"
		}
		for _, h := range s.Helpers() {
			if !slices.ContainsFunc(helpers, func(o Helper) bool { return o.Name == h.Name }) {
				helpers = append(helpers, h)
			}
		}
		fmt.Fprintf(&conds, "\tfunc(v string) bool {\n\t\tif %s {\n\t\t\treturn false\n\t\t}\n\t\treturn true\n\t}, // %d %s\n", cond, i, s.Name)
	}
	SortHelpers(helpers)
	for _, h := range helpers {
		funcs.WriteString(h.Code + "\n\n")
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

%s`, strings.Join(quoted, "\n\t"), conds.String(), strings.Join(samples, ", "), funcs.String())

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
