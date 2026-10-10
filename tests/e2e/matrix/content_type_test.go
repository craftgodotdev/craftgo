package matrix

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// A JSON-body route answers 415 to a body of another media type, the bodies a cross-site form or
// text/plain post sends, and takes application/json or a +json type in any case and with any
// parameters, or a body without a Content-Type.
func TestJSONBodyMediaType(t *testing.T) {
	ts := bootAll(t)
	put := func(contentType string) (int, string) {
		req, err := http.NewRequest(http.MethodPut, ts.URL+"/api/presence/things/t1", strings.NewReader(`{"name":"n"}`))
		if err != nil {
			t.Fatal(err)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, strings.TrimSpace(string(raw))
	}
	for _, ct := range []string{"text/plain", "text/plain; charset=utf-8", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x", "application/jsonx", "text/json", "application/+json"} {
		if code, body := put(ct); code != http.StatusUnsupportedMediaType || body != `{"message":"unsupported media type"}` {
			t.Errorf("%q: got %d %s, want 415", ct, code, body)
		}
	}
	for _, ct := range []string{"", "application/json", "application/json; charset=utf-8", "Application/JSON", "application/merge-patch+json", "application/vnd.api+json;v=1"} {
		if code, body := put(ct); code != http.StatusOK {
			t.Errorf("%q: got %d %s, want 200", ct, code, body)
		}
	}
}

// A raw request reads its own body, whatever its media type.
func TestRawRequestTakesAnyMediaType(t *testing.T) {
	ts := bootAll(t)
	resp, err := http.Post(ts.URL+"/api/raw/rq", "text/plain", strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated || !strings.Contains(string(raw), `"accepted":5`) {
		t.Errorf("got %d %s, want 201 with the body's 5 bytes", resp.StatusCode, raw)
	}
}
