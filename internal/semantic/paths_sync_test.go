package semantic

import (
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/server"
)

// defaultHealthPaths matches pkg/server's default liveness and readiness paths.
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
