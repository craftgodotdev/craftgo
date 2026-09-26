package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile writes content to path, failing the test on error.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// loadManifest loads body as the manifest of a fresh directory.
func loadManifest(t *testing.T, body string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), Filename)
	writeFile(t, path, body)
	return Load(path)
}
