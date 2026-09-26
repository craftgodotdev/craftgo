package matrix

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// A required list or map the logic leaves nil goes out as [] or {} wherever
// it sits, an optional one is left out and a @nullable one stays null; an
// error body is filled the same way.
func TestNilListsGoOutEmpty(t *testing.T) {
	ts := bootAll(t)
	get := func(path string) (int, string) {
		t.Helper()
		resp, err := http.Get(ts.URL + "/api/empty-lists" + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}
	code, body := get("/shapes")
	if code != http.StatusOK {
		t.Fatalf("shapes: %d %s", code, body)
	}
	for _, want := range []string{
		`"leaf":{"tags":[],"meta":{}}`,
		`"leafOpt":{"tags":[],"meta":{}}`,
		`"leaves":[{"tags":[],"meta":{}}]`,
		`"byName":{"a":{"tags":[],"meta":{}}}`,
		`"grid":[[]]`,
		`"nulls":null`,
		`"blob":""`,
		`"boxed":{"v":{"tags":[],"meta":{}},"vs":[]}`,
		`"boxedOpt":{"v":{"tags":[],"meta":{}},"vs":[{"tags":[],"meta":{}}]}`,
		`"tree":{"name":"","kids":[{"name":"","kids":[]}],"next":{"name":"","kids":[]}}`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("shapes body lacks %s:\n%s", want, body)
		}
	}
	for _, group := range []string{`"g":[]`, `"h":[{"tags":[],"meta":{}}]`} {
		if !strings.Contains(body, group) {
			t.Errorf("shapes groups lack %s:\n%s", group, body)
		}
	}
	if strings.Contains(body, `"maybe"`) {
		t.Errorf("an optional nil list must be left out:\n%s", body)
	}
	code, body = get("/clash")
	if code != http.StatusConflict || !strings.Contains(body, `"items":[]`) || !strings.Contains(body, `"tags":[]`) || !strings.Contains(body, `"meta":{}`) {
		t.Errorf("clash: got %d %s, want 409 with empty lists", code, body)
	}
	if code, body = get("/cycle"); code != http.StatusInternalServerError {
		t.Errorf("cycle: got %d %s, want 500", code, body)
	}
}
