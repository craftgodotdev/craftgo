package strfmt

import (
	"strings"
	"testing"
)

// TestSpecsAreWellFormed checks that every spec has a unique name, a label and
// exactly one check.
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
		}
	}
	if got := OpenAPIFormat("datetime"); got != "date-time" {
		t.Errorf("OpenAPIFormat(datetime) = %q, want date-time", got)
	}
	if got := OpenAPIFormat("email"); got != "email" {
		t.Errorf("OpenAPIFormat(email) = %q, want the name itself", got)
	}
}
