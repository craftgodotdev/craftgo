package matrix

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
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
	if code, body = get("/nothing"); code != http.StatusOK || strings.TrimSpace(body) != "null" {
		t.Errorf("nothing: got %d %s, want 200 null", code, body)
	}
}

// A value nested 1500 deep with no cycle is filled throughout, the lists
// visited after its deepest branch included.
func TestNilListsDeepValue(t *testing.T) {
	ts := bootAll(t)
	resp, err := http.Get(ts.URL + "/api/empty-lists/deep")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw)
	if resp.StatusCode != http.StatusOK || !strings.HasSuffix(strings.TrimSpace(body), `"labels":[],"votes":{}}`) || strings.Contains(body, "null") {
		t.Errorf("deep: got %d ...%s", resp.StatusCode, body[max(len(body)-80, 0):])
	}
}

// A value holding itself, through its kids twice over and its next, stops
// the fill at once and answers 500, as the encoder refuses it.
func TestNilListsCycleAnswers500(t *testing.T) {
	ts := bootAll(t)
	done := make(chan int, 1)
	go func() {
		resp, err := http.Get(ts.URL + "/api/empty-lists/cycle")
		if err != nil {
			done <- 0
			return
		}
		resp.Body.Close()
		done <- resp.StatusCode
	}()
	select {
	case code := <-done:
		if code != http.StatusInternalServerError {
			t.Errorf("cycle: got %d, want 500", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cycle: no answer in 10s")
	}
}

// A value every request shares, filled by the first, is only read by the
// next ones, however many run at once.
func TestNilListsSharedValue(t *testing.T) {
	ts := bootAll(t)
	get := func() (int, string) {
		resp, err := http.Get(ts.URL + "/api/empty-lists/shared")
		if err != nil {
			return 0, err.Error()
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}
	if code, body := get(); code != http.StatusOK || !strings.Contains(body, `"g":[]`) {
		t.Fatalf("first: got %d %s", code, body)
	}
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			if code, body := get(); code != http.StatusOK || !strings.Contains(body, `"g":[]`) {
				t.Errorf("shared: got %d %s", code, body)
			}
		})
	}
	wg.Wait()
}
