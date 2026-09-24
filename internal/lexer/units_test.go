package lexer

import (
	"math"
	"testing"
	"time"
)

// TestUnitsAreTheOnlyLexableSuffixes pins that exactly the listed suffixes lex
// as a Duration or a Size.
func TestUnitsAreTheOnlyLexableSuffixes(t *testing.T) {
	for _, u := range DurationUnits {
		if got := first(t, "1"+u).Kind; got != Duration {
			t.Errorf("1%s: got %v, want Duration", u, got)
		}
	}
	for _, u := range SizeUnits {
		if got := first(t, "1"+u.Suffix).Kind; got != Size {
			t.Errorf("1%s: got %v, want Size", u.Suffix, got)
		}
	}
	for _, bad := range []string{"1TB", "1PB", "1d", "1sec", "1kb", "1mb", "1gb", "1Mb"} {
		if got := first(t, bad).Kind; got != Error {
			t.Errorf("%s: got %v, want Error", bad, got)
		}
	}
}

// TestDurationUnitsParse pins that time.ParseDuration accepts every duration
// suffix.
func TestDurationUnitsParse(t *testing.T) {
	for _, u := range DurationUnits {
		if _, err := time.ParseDuration("1" + u); err != nil {
			t.Errorf("%q lexes as a duration but time.ParseDuration rejects it: %v", u, err)
		}
	}
}

func TestSizeMultiplier(t *testing.T) {
	for _, c := range []struct {
		suffix string
		want   int64
	}{{"B", 1}, {"KB", 1024}, {"MB", 1 << 20}, {"GB", 1 << 30}} {
		got, ok := SizeMultiplier(c.suffix)
		if !ok || got != c.want {
			t.Errorf("SizeMultiplier(%q) = %d,%v want %d,true", c.suffix, got, ok, c.want)
		}
	}
	if _, ok := SizeMultiplier("TB"); ok {
		t.Error("TB has no multiplier and must not report one")
	}
}

func TestSizeSuffixesMatchUnits(t *testing.T) {
	got := SizeSuffixes()
	if len(got) != len(SizeUnits) {
		t.Fatalf("SizeSuffixes() has %d entries, SizeUnits has %d", len(got), len(SizeUnits))
	}
	for i, u := range SizeUnits {
		if got[i] != u.Suffix {
			t.Errorf("SizeSuffixes()[%d] = %q, want %q", i, got[i], u.Suffix)
		}
	}
}

func TestParseSize(t *testing.T) {
	for _, c := range []struct {
		in   string
		want int64
	}{
		{"1024", 1024},
		{"0", 0},
		{"-5", -5},
		{"1B", 1},
		{"1KB", 1024},
		{"5MB", 5 << 20},
		{"10GB", 10 << 30},
		{"1.5GB", 1<<30 + 1<<29},
		{"0.5KB", 512},
	} {
		got, ok := ParseSize(c.in)
		if !ok || got != c.want {
			t.Errorf("ParseSize(%q) = %d,%v want %d,true", c.in, got, ok, c.want)
		}
	}
}

// TestParseSizeRejects pins ok=false for an unknown suffix, a missing or
// malformed number, and a product past int64.
func TestParseSizeRejects(t *testing.T) {
	for _, in := range []string{
		"", "   ", "MB", "B", "xMB", "1.2.3MB", "1TB",
		"99999999999999999999GB",
		"9223372036854775807KB",
	} {
		if got, ok := ParseSize(in); ok {
			t.Errorf("ParseSize(%q) = %d,true; want ok=false", in, got)
		}
	}
}

func TestParseSizeMaxInt64(t *testing.T) {
	got, ok := ParseSize("9223372036854775807B")
	if !ok || got != math.MaxInt64 {
		t.Errorf("ParseSize(MaxInt64 B) = %d,%v want %d,true", got, ok, int64(math.MaxInt64))
	}
}
