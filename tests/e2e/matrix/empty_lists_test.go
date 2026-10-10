package matrix

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// emptyListsGet answers GET /api/empty-lists<path> with its status and body.
func emptyListsGet(t *testing.T, url, path string) (int, string) {
	t.Helper()
	resp, err := http.Get(url + "/api/empty-lists" + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// A response goes out as the logic returns it, wherever a list or map sits: a nil one is null,
// an empty one [] or {}, and an optional nil one is left out; an error body likewise.
func TestListsGoOutAsTheLogicReturnsThem(t *testing.T) {
	ts := bootAll(t)
	code, body := emptyListsGet(t, ts.URL, "/shapes")
	if code != http.StatusOK {
		t.Fatalf("shapes: %d %s", code, body)
	}
	for _, want := range []string{
		`"leaf":{"tags":[],"meta":{}}`,
		`"leafOpt":{"tags":null,"meta":null}`,
		`"leaves":[{"tags":[],"meta":null}]`,
		`"byName":{"a":{"tags":null,"meta":null}}`,
		`"groups":{"g":null,"h":[]}`,
		`"grid":[null,[]]`,
		`"nulls":null`,
		`"blob":null`,
		`"boxed":{"v":{"tags":null,"meta":null},"vs":[]}`,
		`"boxedOpt":{"v":{"tags":null,"meta":null},"vs":[{"tags":null,"meta":null}]}`,
		`"tree":{"name":"","kids":[{"name":"","kids":null}],"next":{"name":"","kids":[]}}`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("shapes body lacks %s:\n%s", want, body)
		}
	}
	if strings.Contains(body, `"maybe"`) {
		t.Errorf("an optional nil list must be left out:\n%s", body)
	}
	code, body = emptyListsGet(t, ts.URL, "/clash")
	if want := `{"items":[],"tags":null,"meta":null}`; code != http.StatusConflict || strings.TrimSpace(body) != want {
		t.Errorf("clash: got %d %s, want 409 %s", code, body, want)
	}
	if code, body = emptyListsGet(t, ts.URL, "/nothing"); code != http.StatusOK || strings.TrimSpace(body) != "null" {
		t.Errorf("nothing: got %d %s, want 200 null", code, body)
	}
}

// A value nested 1500 deep with no cycle goes out whole.
func TestListsDeepValueGoesOutWhole(t *testing.T) {
	ts := bootAll(t)
	code, body := emptyListsGet(t, ts.URL, "/deep")
	if code != http.StatusOK || strings.Count(body, `"next":`) != 1500 || !strings.HasSuffix(strings.TrimSpace(body), `"labels":null,"votes":null}`) {
		t.Errorf("deep: got %d ...%s", code, body[max(len(body)-80, 0):])
	}
}

// A value holding itself has no JSON form, so the encoder refuses it and the client gets 500.
func TestListsCycleAnswers500(t *testing.T) {
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

// A value every request shares is only read: requests running at once from the first one on
// all get it as the logic holds it, its nil lists null.
func TestListsSharedValueIsOnlyRead(t *testing.T) {
	ts := bootAll(t)
	const want = `"groups":{"g":null}`
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			resp, err := http.Get(ts.URL + "/api/empty-lists/shared")
			if err != nil {
				t.Error(err)
				return
			}
			defer resp.Body.Close()
			raw, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), want) || !strings.Contains(string(raw), `"leaves":null`) {
				t.Errorf("shared: got %d %s", resp.StatusCode, raw)
			}
		})
	}
	wg.Wait()
}
