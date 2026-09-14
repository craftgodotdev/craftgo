// The number-suffix vocabulary: which suffixes a numeric literal may carry,
// and what a size suffix is worth in bytes.
package lexer

import (
	"strconv"
	"strings"
	"time"
)

// SizeUnit pairs a size suffix with its byte multiplier. The multiplier
// travels with the suffix because every consumer of a size literal needs
// both: a suffix that lexes but has no factor produces a byte count of
// zero, and the emitters read zero as "no cap" and emit no check at all.
type SizeUnit struct {
	Suffix string
	Bytes  int64
}

// SizeUnits is the size vocabulary, longest suffix first so a suffix match
// picks "MB" before "B".
var SizeUnits = []SizeUnit{
	{"GB", 1 << 30},
	{"MB", 1 << 20},
	{"KB", 1 << 10},
	{"B", 1},
}

// DurationUnits is the duration vocabulary. Every entry is a suffix
// [time.ParseDuration] accepts, which is what [ParseDuration] converts
// with (TestDurationUnitsParse pins the two lists to each other).
var DurationUnits = []string{"ns", "us", "µs", "ms", "s", "m", "h"}

// SizeSuffixes returns the size suffixes alone, in [SizeUnits] order.
func SizeSuffixes() []string {
	out := make([]string, len(SizeUnits))
	for i, u := range SizeUnits {
		out[i] = u.Suffix
	}
	return out
}

// IsDurationSuffix reports whether s is a legal duration suffix.
func IsDurationSuffix(s string) bool {
	for _, u := range DurationUnits {
		if u == s {
			return true
		}
	}
	return false
}

// SizeMultiplier returns the byte multiplier for size suffix s.
func SizeMultiplier(s string) (int64, bool) {
	for _, u := range SizeUnits {
		if u.Suffix == s {
			return u.Bytes, true
		}
	}
	return 0, false
}

// ParseDuration converts the source text of a duration literal (`30s`,
// `1.5h`) into a time.Duration. Returns ok=false for a suffix outside
// [DurationUnits] and for a value that overflows the duration range.
func ParseDuration(text string) (time.Duration, bool) {
	d, err := time.ParseDuration(strings.TrimSpace(text))
	if err != nil {
		return 0, false
	}
	return d, true
}

// ParseSize converts the source text of a size literal (`5MB`, `1.5GB`,
// `1024B`) into a byte count. A bare number is bytes, matching the DSL's
// "bare number → bytes" rule. Returns ok=false for an unknown suffix, a
// missing or malformed number, and a value that does not fit an int64.
func ParseSize(text string) (int64, bool) {
	t := strings.TrimSpace(text)
	if t == "" {
		return 0, false
	}
	for _, u := range SizeUnits {
		if !strings.HasSuffix(t, u.Suffix) {
			continue
		}
		return scaleSize(strings.TrimSpace(strings.TrimSuffix(t, u.Suffix)), u.Bytes)
	}
	return scaleSize(t, 1)
}

// scaleSize multiplies a literal's numeric part by a unit's byte factor.
// Integers multiply exactly; a fractional part goes through float64 and
// truncates. A product outside the int64 range reports ok=false rather than
// wrapping or saturating, so the caller diagnoses the literal instead of
// enforcing a cap nobody wrote.
func scaleSize(num string, mult int64) (int64, bool) {
	if num == "" {
		return 0, false
	}
	if n, err := strconv.ParseInt(num, 10, 64); err == nil {
		p := n * mult
		if n != 0 && p/n != mult {
			return 0, false
		}
		return p, true
	}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, false
	}
	// 2^63 is the first magnitude an int64 cannot hold. NaN fails both
	// comparisons and so is rejected by the same guard.
	const outOfRange = float64(1 << 63)
	scaled := f * float64(mult)
	if !(scaled > -outOfRange && scaled < outOfRange) {
		return 0, false
	}
	return int64(scaled), true
}
