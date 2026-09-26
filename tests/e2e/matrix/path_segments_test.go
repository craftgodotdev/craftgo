package matrix

import (
	"encoding/json"
	"net/http"
	"testing"
)

// A route whose literal segments hold dots and digits is served, its
// variable bound.
func TestLiteralSegmentsWithDotsAndDigits(t *testing.T) {
	ts := bootAll(t)
	resp, err := http.Get(ts.URL + "/api/bindings/.well-known/v1.0/2fa/k1/keys.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var item struct{ Name string }
	if err := json.NewDecoder(resp.Body).Decode(&item); resp.StatusCode != http.StatusOK || err != nil || item.Name != "k1" {
		t.Errorf("got %d %+v (%v), want 200 naming k1", resp.StatusCode, item, err)
	}
}
