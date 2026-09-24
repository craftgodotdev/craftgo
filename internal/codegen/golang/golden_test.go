package golang

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// updateGolden (-update) makes expectGolden rewrite the testdata/golden files.
var updateGolden = flag.Bool("update", false, "rewrite golden snapshot files instead of comparing")

// expectGolden compares actual with testdata/golden/<name>, or rewrites that file under -update.
func expectGolden(t *testing.T, name, actual string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir testdata/golden: %v", err)
		}
		if err := os.WriteFile(path, []byte(actual), 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		t.Logf("wrote golden %s (%d bytes)", path, len(actual))
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.Fatalf("golden file %s does not exist; run with -update to create it", path)
		}
		t.Fatalf("read golden %s: %v", path, err)
	}
	// A Windows checkout may give the golden file CRLF line endings; generated output is LF.
	wantStr := strings.ReplaceAll(string(want), "\r\n", "\n")
	if wantStr == actual {
		return
	}
	t.Errorf("golden mismatch (%s) - diff first divergence:\n%s", path, firstDiff(wantStr, actual))
}

// firstDiff returns the want and got lines around their first difference.
func firstDiff(want, got string) string {
	wantLines := strings.SplitSeq(want, "\n")
	gotLines := strings.SplitSeq(got, "\n")
	wIter, gIter := wantLines, gotLines
	wantSlice := stringsCollect(wIter)
	gotSlice := stringsCollect(gIter)
	max := len(wantSlice)
	if len(gotSlice) > max {
		max = len(gotSlice)
	}
	for i := 0; i < max; i++ {
		w, g := "", ""
		if i < len(wantSlice) {
			w = wantSlice[i]
		}
		if i < len(gotSlice) {
			g = gotSlice[i]
		}
		if w != g {
			start := i - 2
			if start < 0 {
				start = 0
			}
			end := i + 4
			if end > max {
				end = max
			}
			var sb strings.Builder
			for j := start; j < end; j++ {
				marker := "  "
				if j == i {
					marker = "→ "
				}
				wj, gj := "", ""
				if j < len(wantSlice) {
					wj = wantSlice[j]
				}
				if j < len(gotSlice) {
					gj = gotSlice[j]
				}
				sb.WriteString(marker)
				sb.WriteString("want: ")
				sb.WriteString(wj)
				sb.WriteString("\n")
				sb.WriteString(marker)
				sb.WriteString("got:  ")
				sb.WriteString(gj)
				sb.WriteString("\n")
			}
			return sb.String()
		}
	}
	return "(strings differ in length but match line-by-line up to the shorter end)"
}

// stringsCollect drains a strings.SplitSeq iterator into a slice.
func stringsCollect(it func(yield func(string) bool)) []string {
	var out []string
	it(func(s string) bool {
		out = append(out, s)
		return true
	})
	return out
}
