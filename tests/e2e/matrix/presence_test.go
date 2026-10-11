package matrix

import (
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/server"
)

// presenceOrder returns a body carrying every member of presence.Order.
func presenceOrder() map[string]any {
	return map[string]any{
		"by":     "ana",
		"name":   "n",
		"count":  2,
		"active": true,
		"grade":  "low",
		"at":     "2026-10-10T10:00:00Z",
		"alias":  "al",
		"spot":   map[string]any{"coords": map[string]any{"lat": 1.5}},
		"meta":   map[string]any{"coords": map[string]any{"lat": 2.5, "zoom": 9}},
		"items":  []any{map[string]any{"sku": "a"}},
		"byName": map[string]any{"k": map[string]any{"sku": "b"}},
		"boxed":  map[string]any{"v": map[string]any{"sku": "c"}},
		"listed": map[string]any{"v": []any{map[string]any{"sku": "d"}}},
		"doc":    map[string]any{"x": 1},
	}
}

// postPresence posts body as JSON to the presence echo route.
func postPresence(t *testing.T, url string, body any) (int, string) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(url+"/api/presence/orders", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

// with returns a copy of body with key set to v, or left out when v is absent.
func with(body map[string]any, key string, v any) map[string]any {
	out := maps.Clone(body)
	if v == absent {
		delete(out, key)
	} else {
		out[key] = v
	}
	return out
}

// absent marks a member [with] leaves out.
var absent = new(int)

// A member declared without `?` must come and not be null, a mixin's included; @nullable lets
// one be null, an optional one may stay out, and a raw one counts null as missing.
func TestBodyMembersWithoutQuestionMarkAreRequired(t *testing.T) {
	ts := bootAll(t)
	if code, body := postPresence(t, ts.URL, presenceOrder()); code != http.StatusCreated {
		t.Fatalf("a complete body: got %d %s", code, body)
	}
	for _, key := range []string{"by", "name", "count", "active", "grade", "at", "spot", "items", "byName", "boxed", "listed", "doc"} {
		for _, v := range []any{absent, nil} {
			code, body := postPresence(t, ts.URL, with(presenceOrder(), key, v))
			if want := `{"message":"` + key + `: required"}`; code != http.StatusBadRequest || strings.TrimSpace(body) != want {
				t.Errorf("%s as %v: got %d %s, want 400 %s", key, v, code, body, want)
			}
		}
	}
	for _, key := range []string{"alias", "meta"} {
		code, body := postPresence(t, ts.URL, with(presenceOrder(), key, absent))
		if want := `{"message":"` + key + `: required"}`; code != http.StatusBadRequest || strings.TrimSpace(body) != want {
			t.Errorf("%s absent: got %d %s, want 400 %s", key, code, body, want)
		}
		if code, body := postPresence(t, ts.URL, with(presenceOrder(), key, nil)); code != http.StatusCreated || !strings.Contains(body, `"`+key+`":null`) {
			t.Errorf("%s null: got %d %s, want 201 with it null", key, code, body)
		}
	}
	for _, key := range []string{"note", "extra"} {
		for _, v := range []any{absent, nil} {
			if code, body := postPresence(t, ts.URL, with(presenceOrder(), key, v)); code != http.StatusCreated {
				t.Errorf("optional %s as %v: got %d %s, want 201", key, v, code, body)
			}
		}
	}
}

// An empty string, zero, false, an empty list and an empty map are values, not missing ones.
func TestBodyZeroValuesArePresent(t *testing.T) {
	ts := bootAll(t)
	body := presenceOrder()
	body["name"], body["count"], body["active"], body["items"], body["byName"] = "", 0, false, []any{}, map[string]any{}
	code, out := postPresence(t, ts.URL, body)
	if code != http.StatusCreated {
		t.Fatalf("zero values: got %d %s, want 201", code, out)
	}
	for _, want := range []string{`"name":""`, `"count":0`, `"active":false`, `"items":[]`, `"byName":{}`} {
		if !strings.Contains(out, want) {
			t.Errorf("echo lacks %s: %s", want, out)
		}
	}
}

// A member missing below the root is named by its JSON path, without an index or a map key, through
// nested types, list elements, map values, generic instances and a @nullable struct.
func TestBodyMissingMemberPath(t *testing.T) {
	ts := bootAll(t)
	for _, c := range []struct {
		key  string
		v    any
		want string
	}{
		{"spot", map[string]any{}, "spot.coords"},
		{"spot", map[string]any{"coords": map[string]any{}}, "spot.coords.lat"},
		{"meta", map[string]any{"coords": map[string]any{"zoom": 1}}, "meta.coords.lat"},
		{"items", []any{map[string]any{"sku": "a"}, map[string]any{"qty": 1}}, "items.sku"},
		{"byName", map[string]any{"k": map[string]any{"sku": nil}}, "byName.sku"},
		{"boxed", map[string]any{}, "boxed.v"},
		{"boxed", map[string]any{"v": map[string]any{"sku": "x"}, "opt": map[string]any{}}, "boxed.opt.sku"},
		{"listed", map[string]any{"v": []any{map[string]any{}}}, "listed.v.sku"},
		{"listed", map[string]any{"v": []any{}, "opt": []any{map[string]any{}}}, "listed.opt.sku"},
	} {
		code, body := postPresence(t, ts.URL, with(presenceOrder(), c.key, c.v))
		if want := `{"message":"` + c.want + `: required"}`; code != http.StatusBadRequest || strings.TrimSpace(body) != want {
			t.Errorf("%s = %v: got %d %s, want 400 %s", c.key, c.v, code, body, want)
		}
	}
}

// A nested @default fills its member when the member is absent and not when it is null; a value
// overrides it.
func TestBodyNestedDefault(t *testing.T) {
	ts := bootAll(t)
	for _, c := range []struct {
		coords map[string]any
		want   string
	}{
		{map[string]any{"lat": 1}, `"coords":{"lat":1,"zoom":3}`},
		{map[string]any{"lat": 1, "zoom": nil}, `"coords":{"lat":1}`},
		{map[string]any{"lat": 1, "zoom": 7}, `"coords":{"lat":1,"zoom":7}`},
	} {
		code, body := postPresence(t, ts.URL, with(presenceOrder(), "spot", map[string]any{"coords": c.coords}))
		if code != http.StatusCreated || !strings.Contains(body, c.want) {
			t.Errorf("coords %v: got %d %s, want 201 with %s", c.coords, code, body, c.want)
		}
	}
}

// A null body has none of the required members; a value of the wrong type below a @nullable member
// is named by its path.
func TestBodyNullAndNullableMismatch(t *testing.T) {
	ts := bootAll(t)
	resp, err := http.Post(ts.URL+"/api/presence/orders", "application/json", strings.NewReader("null"))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || strings.TrimSpace(string(raw)) != `{"message":"by: required"}` {
		t.Errorf("null body: got %d %s, want 400 by: required", resp.StatusCode, raw)
	}
	code, body := postPresence(t, ts.URL, with(presenceOrder(), "alias", 5))
	if want := `{"message":"alias: expected string, got number"}`; code != http.StatusBadRequest || strings.TrimSpace(body) != want {
		t.Errorf("alias 5: got %d %s, want 400 %s", code, body, want)
	}
}

// A member the route fills need not come in the body.
func TestBodyMemberTheRouteFills(t *testing.T) {
	ts := bootAll(t)
	put := func(body string) (int, string) {
		req, err := http.NewRequest(http.MethodPut, ts.URL+"/api/presence/things/t1", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, strings.TrimSpace(string(raw))
	}
	if code, body := put(`{"name":"n"}`); code != http.StatusOK || body != `{"id":"t1","name":"n"}` {
		t.Errorf("without id: got %d %s, want 200 with the route's id", code, body)
	}
	if code, body := put(`{}`); code != http.StatusBadRequest || body != `{"message":"name: required"}` {
		t.Errorf("without name: got %d %s, want 400 name: required", code, body)
	}
}

// A required text part of a multipart form must come.
func TestFormRequiresTextPart(t *testing.T) {
	ts := bootAll(t)
	send := func(title bool) (int, string) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		if title {
			_ = mw.WriteField("title", "")
		}
		fw, _ := mw.CreateFormFile("file", "a.txt")
		_, _ = fw.Write([]byte("hi"))
		_ = mw.Close()
		resp, err := http.Post(ts.URL+"/api/presence/uploads", mw.FormDataContentType(), &buf)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, strings.TrimSpace(string(raw))
	}
	if code, body := send(false); code != http.StatusBadRequest || body != `{"message":"title: missing required form parameter"}` {
		t.Errorf("without title: got %d %s", code, body)
	}
	if code, body := send(true); code != http.StatusNoContent {
		t.Errorf("with an empty title: got %d %s, want 204", code, body)
	}
}

// Strict JSON takes the members of the request type, a mixin's and a @nullable one's included, and
// refuses any other at every depth.
func TestBodyPresenceUnderStrictJSON(t *testing.T) {
	ts := bootAll(t)
	if err := server.SetStrictJSON(true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.SetStrictJSON(false) })
	if code, body := postPresence(t, ts.URL, presenceOrder()); code != http.StatusCreated {
		t.Fatalf("a complete body: got %d %s", code, body)
	}
	for _, c := range []struct {
		key string
		v   any
	}{
		{"bogus", 1},
		{"spot", map[string]any{"coords": map[string]any{"lat": 1}, "bogus": 1}},
		{"meta", map[string]any{"coords": map[string]any{"lat": 1}, "bogus": 1}},
	} {
		if code, body := postPresence(t, ts.URL, with(presenceOrder(), c.key, c.v)); code != http.StatusBadRequest || !strings.Contains(body, "bogus: unknown field") {
			t.Errorf("%s: got %d %s, want 400 naming bogus", c.key, code, body)
		}
	}
}
