package docs

import (
	"slices"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/config"
)

// A raw request without a block declares every path variable of its route,
// in route order, the service @prefix's included.
func TestRawRequestDeclaresEveryPathVariable(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"s/s.craftgo": `package s
type IngestResult { bytes int }
@prefix("/stream/{tenant}")
service StreamService {
	@passthrough
	get TailLogs /logs/{service} {}

	@rawRequest
	post Ingest /ingest { response IngestResult }
}`,
	}, &config.Config{})
	for path, want := range map[string][]string{
		"/stream/{tenant}/logs/{service}": {"tenant", "service"},
		"/stream/{tenant}/ingest":         {"tenant"},
	} {
		item := doc.Paths.Find(path)
		if item == nil {
			t.Fatalf("no path %s", path)
		}
		var got []string
		for _, op := range item.Operations() {
			for _, p := range op.Parameters {
				if p.Value.In == "path" && p.Value.Required {
					got = append(got, p.Value.Name)
				}
			}
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s declares path parameters %v, want %v", path, got, want)
		}
	}
}
