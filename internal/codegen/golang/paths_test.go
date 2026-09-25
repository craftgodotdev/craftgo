package golang

import "testing"

func TestRelDirNormalises(t *testing.T) {
	for in, want := range map[string]string{
		"":                   "",
		".":                  "",
		"./":                 "",
		"./internal/config/": "internal/config",
		"internal\\config":   "internal/config",
		"./x/../cfg":         "cfg",
		"./a/./b":            "a/b",
	} {
		if got := relDir(in); got != want {
			t.Errorf("relDir(%q) = %q, want %q", in, got, want)
		}
	}
	if got := goImportFromRel("example.com/x", "."); got != "example.com/x" {
		t.Errorf("goImportFromRel(., .) = %q, want the module path", got)
	}
	if got := displayDir("."); got != "." {
		t.Errorf("displayDir(.) = %q, want .", got)
	}
}
