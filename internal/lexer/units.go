package lexer

import (
	"slices"
	"strconv"
	"strings"
	"time"
)

// SizeUnit is a size suffix and its byte multiplier.
type SizeUnit struct {
	Suffix string
	Bytes  int64
}

// SizeUnits lists the size suffixes, longest first so a suffix match tries "MB"
// before "B".
var SizeUnits = []SizeUnit{
	{"GB", 1 << 30},
	{"MB", 1 << 20},
	{"KB", 1 << 10},
	{"B", 1},
}

// DurationUnits lists the duration suffixes, each one [time.ParseDuration]
// accepts.
var DurationUnits = []string{"ns", "us", "µs", "ms", "s", "m", "h"}

// SizeSuffixes returns the size suffixes in [SizeUnits] order.
func SizeSuffixes() []string {
	out := make([]string, len(SizeUnits))
	for i, u := range SizeUnits {
		out[i] = u.Suffix
	}
	return out
}

// IsDurationSuffix reports whether s is a duration suffix.
func IsDurationSuffix(s string) bool {
	return slices.Contains(DurationUnits, s)
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

// ParseDuration converts a duration literal's text (`30s`, `1.5h`) with
// [time.ParseDuration]; ok is false when that fails.
func ParseDuration(text string) (time.Duration, bool) {
	d, err := time.ParseDuration(strings.TrimSpace(text))
	if err != nil {
		return 0, false
	}
	return d, true
}

// ParseSize converts a size literal's text (`5MB`, `1.5GB`, or a bare byte
// count) into bytes; ok is false when it is malformed or overflows int64.
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

// scaleSize multiplies num by mult, exactly for an integer and truncating for a
// fraction; ok is false outside the int64 range.
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
	const outOfRange = float64(1 << 63)
	scaled := f * float64(mult)
	// NaN fails both comparisons, so the guard rejects it too.
	if !(scaled > -outOfRange && scaled < outOfRange) {
		return 0, false
	}
	return int64(scaled), true
}
