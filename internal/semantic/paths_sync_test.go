package semantic

import (
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/server"
)

// The analyser mirrors the runtime's default health routes instead of
// importing pkg/server (the analyzer stays runtime-free). This pins the mirror
// to the exported runtime constants so a change to either side fails here
// instead of silently letting a user route collide with a live health path.
func TestHealthPathsMatchRuntime(t *testing.T) {
	want := []string{server.DefaultLivenessPath, server.DefaultReadinessPath}
	if len(defaultHealthPaths) != len(want) {
		t.Fatalf("defaultHealthPaths = %v, runtime defaults = %v", defaultHealthPaths, want)
	}
	for i, p := range want {
		if defaultHealthPaths[i] != p {
			t.Errorf("defaultHealthPaths[%d] = %q, runtime default = %q", i, defaultHealthPaths[i], p)
		}
	}
}
